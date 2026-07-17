// Command big2-tui runs a single-room Big 2 game over SSH. Others join with
// `ssh <host> -p <port>`; once the game starts, new connections are turned away.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	clog "github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/prefs"
	"github.com/Avangelista/big2-tui/internal/room"
	"github.com/Avangelista/big2-tui/internal/tui"
)

func main() {
	port := flag.Int("port", 2222, "SSH port to listen on")
	hostKey := flag.String("host-key", ".ssh/big2-tui_ed25519", "SSH host key path (created if missing)")
	serveOnly := flag.Bool("serve-only", false, "run headless (no local host player); the first person to join becomes host")
	flag.Parse()

	// Keep logs off the host's terminal so they don't corrupt the TUI.
	if f, err := os.OpenFile("big2-tui.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		clog.SetOutput(f)
		defer f.Close()
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Preferences (house rules, reaction labels, remembered letters) persist beside the
	// binary across runs.
	prefsPath := prefs.DefaultPath()
	saved := prefs.Load(prefsPath)
	r := room.New(4, 2, rng, // up to 4 seats; the host may start with 2+
		room.WithSavedPrefs(saved.Rules, saved.Reactions, saved.Letters),
		room.WithPersister(newFilePrefs(prefsPath)),
	)
	joinHint := fmt.Sprintf("ssh -p %d %s", *port, localIP())

	srv, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf(":%d", *port)),
		wish.WithHostKeyPath(*hostKey),
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		wish.WithKeyboardInteractiveAuth(func(ssh.Context, gossh.KeyboardInteractiveChallenge) bool { return true }),
		wish.WithMiddleware(
			bm.MiddlewareWithProgramHandler(sshHandler(r, joinHint), termenv.ANSI256),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to start server:", err)
		os.Exit(1)
	}

	clog.Info("listening", "port", *port, "serveOnly", *serveOnly)

	if *serveOnly {
		fmt.Printf("big2-tui serving on :%d - others join with:\n%s\n", *port, joinHint)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != ssh.ErrServerClosed {
				clog.Error("ssh server", "err", err)
			}
		}()
		<-sig
		r.Close()                          // tell remotes the room is closing (RoomClosedMsg)
		time.Sleep(250 * time.Millisecond) // let it land so their terminals are restored
		_ = srv.Close()                    // force-close promptly, no connWg wait
		return
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != ssh.ErrServerClosed {
			clog.Error("ssh server", "err", err)
		}
	}()

	// The host plays locally in this terminal, joining the same room.
	hostID := room.NewID()
	model := tui.New(r, hostID, joinHint, lipgloss.DefaultRenderer())
	p := tea.NewProgram(model, tea.WithAltScreen())
	model.SetProgram(p)
	r.Submit(room.JoinCmd{ID: hostID, Prog: p, Host: true, Identity: room.LocalIdentity})

	// Quit the host program on SIGINT/SIGTERM too.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; p.Quit() }()

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui error:", err)
	}

	// Host quit: tell remotes the room is closing, then force-close.
	r.Close()
	time.Sleep(250 * time.Millisecond)
	_ = srv.Close()
}

func sshHandler(r *room.Room, joinHint string) bm.ProgramHandler {
	return func(s ssh.Session) *tea.Program {
		id := room.NewID()
		renderer := bm.MakeRenderer(s)
		model := tui.New(r, id, joinHint, renderer)
		opts := append(bm.MakeOptions(s), tea.WithAltScreen())
		p := tea.NewProgram(model, opts...)
		model.SetProgram(p)
		r.Submit(room.JoinCmd{ID: id, Prog: p, Identity: keyIdentity(s.PublicKey())})
		go func() {
			<-s.Context().Done()
			r.Submit(room.DisconnectCmd{ID: id})
		}()
		return p
	}
}

// filePrefs persists room preferences to a JSON file beside the binary. Writes run on a
// dedicated goroutine (latest wins), so a slow or wedged filesystem never blocks the
// room's single actor goroutine, which is what calls Save.
type filePrefs struct {
	path string
	ch   chan prefs.Prefs
}

func newFilePrefs(path string) *filePrefs {
	f := &filePrefs{path: path, ch: make(chan prefs.Prefs, 1)}
	go func() {
		for p := range f.ch {
			if err := prefs.Save(f.path, p); err != nil {
				clog.Warn("save prefs", "err", err)
			}
		}
	}()
	return f
}

// Save enqueues the latest preferences without blocking the caller. If a write is still
// pending it is replaced by this newer state (the room always sends the full state, so a
// dropped intermediate write loses nothing).
func (f *filePrefs) Save(rules game.Rules, reactions []string, letters map[string]string) {
	p := prefs.Prefs{Rules: rules, Reactions: reactions, Letters: letters}
	select {
	case f.ch <- p:
	default: // a write is pending: swap in the newer state
		select {
		case <-f.ch:
		default:
		}
		select {
		case f.ch <- p:
		default:
		}
	}
}

// keyIdentity is a stable per-user id from the client's SSH public key, or "" when the
// client offered none (those players just aren't remembered).
func keyIdentity(pk ssh.PublicKey) string {
	if pk == nil {
		return ""
	}
	return gossh.FingerprintSHA256(pk)
}

// localIP is this machine's primary outbound IP (no packets are sent).
func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
