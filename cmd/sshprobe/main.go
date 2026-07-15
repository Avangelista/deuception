// Command sshprobe connects N clients to a running big2-tui SSH server, each
// requesting a PTY, and checks the rendered frames - a smoke test for SSH auth,
// PTY handling, shared-room rendering, and the host-driven start.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var ansi = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[=>]|\x1b\][0-9);]*`)

var (
	reLabel    = regexp.MustCompile(`([A-Z]) \d+`)   // a player's "L n" label
	reTurn     = regexp.MustCompile(`\[[A-Z] \d+\]`) // the bracketed label = whose turn it is
	rePlayed3D = regexp.MustCompile(`\|3D`)          // the 3D opener sitting in the centre pile
)

func clean(s string) string {
	s = ansi.ReplaceAllString(s, "")
	// collapse runs of blank lines
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		t := strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(t) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}

type client struct {
	name  string
	buf   *lockedBuf
	sess  *ssh.Session
	conn  *ssh.Client
	stdin io.WriteCloser
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func dial(addr, name string) (*client, error) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            name,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	sess, err := conn.NewSession()
	if err != nil {
		return nil, err
	}
	buf := &lockedBuf{}
	pr, pw := io.Pipe()
	sess.Stdout = buf
	sess.Stderr = buf
	sess.Stdin = pr
	if err := sess.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		return nil, err
	}
	// Answer the terminal capability queries Bubble Tea sends at startup (OSC 11
	// background + DA1), as a real terminal would, so it proceeds to first paint.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = pw.Write([]byte("\x1b]11;rgb:1a1a/1a1a/1a1a\x07\x1b[?1;2c"))
	}()
	return &client{name: name, buf: buf, sess: sess, conn: conn, stdin: pw}, nil
}

func main() {
	addr := "localhost:2223"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	names := []string{"alice", "bob", "carol", "dave"}

	var clients []*client
	for _, n := range names {
		c, err := dial(addr, n)
		if err != nil {
			fmt.Printf("dial %s: %v\n", n, err)
			os.Exit(1)
		}
		clients = append(clients, c)
		time.Sleep(200 * time.Millisecond) // stagger joins
	}

	time.Sleep(800 * time.Millisecond)          // waiting room settles
	_, _ = clients[0].stdin.Write([]byte("\r")) // host presses enter to start (no auto-start)
	time.Sleep(1500 * time.Millisecond)         // game renders

	pass, fail := 0, 0
	check := func(cond bool, msg string) {
		if cond {
			pass++
			fmt.Println("  PASS:", msg)
		} else {
			fail++
			fmt.Println("  FAIL:", msg)
		}
	}

	for _, c := range clients {
		raw := c.buf.String()
		frame := clean(raw)
		fmt.Printf("\n===== %s frame [raw %d bytes] =====\n%s\n", c.name, len(raw), frame)
		check(len(strings.TrimSpace(frame)) > 0, c.name+" rendered a non-empty frame")
		// Every viewer sees the same four distinct player letters.
		seen := map[string]bool{}
		for _, mm := range reLabel.FindAllStringSubmatch(frame, -1) {
			seen[mm[1]] = true
		}
		check(len(seen) == 4, fmt.Sprintf("%s sees all four players (got %d)", c.name, len(seen)))
	}

	// After the host presses enter the game starts; exactly one label (in any one
	// frame) should be on turn.
	turns := len(reTurn.FindAllString(clean(clients[0].buf.String()), -1))
	check(turns == 1, fmt.Sprintf("exactly one player is on turn (got %d)", turns))

	// Drive the opener over SSH: every client selects card 0 and tries to play;
	// only the leader (holding 3D) succeeds, so "3D" appears in the centre pile.
	fmt.Println("\n(every client selects card 0 and plays; only the leader's 3D lands)")
	for _, c := range clients {
		_, _ = c.stdin.Write([]byte(" ")) // select cursor card
	}
	time.Sleep(300 * time.Millisecond)
	for _, c := range clients {
		_, _ = c.stdin.Write([]byte("\r")) // enter -> play
	}
	time.Sleep(900 * time.Millisecond)
	after := ""
	for _, c := range clients {
		after += clean(c.buf.String())
	}
	check(rePlayed3D.MatchString(after), "the 3D opener was played and appears in the centre pile")

	fmt.Printf("\n=== sshprobe: %d passed, %d failed ===\n", pass, fail)
	for _, c := range clients {
		_ = c.sess.Close()
		_ = c.conn.Close()
	}
	if fail > 0 {
		os.Exit(1)
	}
}
