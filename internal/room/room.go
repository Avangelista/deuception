// Package room hosts a single, in-memory Big 2 room. One actor goroutine owns
// all mutable state; sessions submit Commands and get per-viewer redacted
// snapshots. Nothing is persisted.
package room

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Avangelista/deuception/internal/bot"
	"github.com/Avangelista/deuception/internal/game"
	"github.com/Avangelista/deuception/internal/protocol"
)

// Seat is one player position in the room. A bot seat is Connected with Bot set
// and a nil Prog, so it is skipped by fanout and never swept as a dropout.
type Seat struct {
	ID        string
	Prog      *tea.Program
	Connected bool
	Host      bool
	Bot       bool
	BotLevel  int  // 1-9 difficulty when Bot
	Letter    byte // chosen display letter, unique per room
	Score     int  // cumulative penalty across hands (lower is better)
}

// Room is a single game room served to many connections.
type Room struct {
	cmds     chan Command
	maxSeats int
	minStart int
	rng      *mrand.Rand
	botDelay time.Duration // how long a bot "thinks" before acting

	// actor-owned state (only touched inside run):
	seats     []*Seat
	game      *game.GameState
	phase     protocol.Phase
	rev       int // monotonic snapshot revision; lets clients drop out-of-order sends
	turnToken int // bumped whenever a bot is scheduled; invalidates stale timers
}

// New starts a room actor. maxSeats caps the table, minStart is the fewest that can start.
func New(maxSeats, minStart int, rng *mrand.Rand) *Room {
	r := &Room{
		cmds:     make(chan Command, 64),
		maxSeats: maxSeats,
		minStart: minStart,
		rng:      rng,
		phase:    protocol.Waiting,
		botDelay: time.Second,
	}
	go r.run()
	return r
}

// Submit enqueues a command for the actor; safe from any goroutine.
func (r *Room) Submit(c Command) {
	defer func() { _ = recover() }() // tolerate submit after close
	r.cmds <- c
}

// NewID returns a random session/player identifier.
func NewID() string {
	b := make([]byte, 8)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Room) run() {
	for c := range r.cmds {
		switch cmd := c.(type) {
		case JoinCmd:
			r.handleJoin(cmd)
		case StartCmd:
			r.handleStart(cmd)
		case PlayCmd:
			r.handlePlay(cmd)
		case PassCmd:
			r.handlePass(cmd)
		case NextHandCmd:
			r.handleNextHand(cmd)
		case SetLetterCmd:
			r.handleSetLetter(cmd)
		case AddBotCmd:
			r.handleAddBot(cmd)
		case RemoveBotCmd:
			r.handleRemoveBot(cmd)
		case BotActCmd:
			r.handleBotAct(cmd)
		case DisconnectCmd:
			r.handleLeave(cmd.ID)
		case QuitCmd:
			r.handleLeave(cmd.ID)
		case queryCmd:
			if idx := r.seatIndexByID(cmd.id); idx >= 0 {
				cmd.reply <- r.snapshotFor(idx)
			} else {
				cmd.reply <- protocol.StateSnapshot{YouSeat: -1}
			}
		case closeCmd:
			for _, s := range r.seats {
				if s.Connected {
					safeSend(s.Prog, protocol.RoomClosedMsg{})
				}
			}
			close(cmd.done)
		}
	}
}

// closeCmd tells every connected player the room is shutting down.
type closeCmd struct{ done chan struct{} }

func (closeCmd) isCmd() {}

// Close notifies connected players and returns once the shutdown message is dispatched.
func (r *Room) Close() {
	done := make(chan struct{})
	r.Submit(closeCmd{done: done})
	<-done
}

// queryCmd requests a synchronous snapshot for a seat (used by tests).
type queryCmd struct {
	id    string
	reply chan protocol.StateSnapshot
}

func (queryCmd) isCmd() {}

// Query returns the redacted snapshot for id, serialized through the actor.
func (r *Room) Query(id string) protocol.StateSnapshot {
	reply := make(chan protocol.StateSnapshot, 1)
	r.Submit(queryCmd{id: id, reply: reply})
	return <-reply
}

func (r *Room) handleJoin(c JoinCmd) {
	// No reconnecting: a returning connection is a fresh id, turned away mid-game.
	// Its old seat stays in play as an auto-passing dropout.
	if r.phase != protocol.Waiting {
		safeSend(c.Prog, protocol.KickedMsg{Reason: "game already in progress"})
		return
	}
	if len(r.seats) >= r.maxSeats {
		safeSend(c.Prog, protocol.KickedMsg{Reason: "room is full"})
		return
	}
	// First to join is host (covers serve-only mode with no local host seat).
	isHost := c.Host || len(r.seats) == 0
	seat := &Seat{ID: c.ID, Prog: c.Prog, Connected: true, Host: isHost, Letter: r.freeLetter()}
	r.seats = append(r.seats, seat)
	r.fanout()
}

func (r *Room) handleStart(c StartCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Waiting {
		return
	}
	if len(r.seats) < r.minStart {
		safeSend(s.Prog, protocol.ErrorMsg{Text: fmt.Sprintf("need at least %d players to start", r.minStart)})
		return
	}
	r.startGame()
	r.afterTransition()
}

func (r *Room) startGame() {
	r.game = game.NewGame(len(r.seats), game.SimpleStraight)
	if err := r.game.Deal(r.rng); err != nil {
		safeSendAll(r.seats, protocol.ErrorMsg{Text: "failed to deal: " + err.Error()})
		return
	}
	r.phase = protocol.InGame
	r.turnToken++ // fresh hand: any leftover bot timer can't match
}

func (r *Room) handlePlay(c PlayCmd) {
	if r.phase != protocol.InGame {
		return
	}
	idx := r.seatIndexByID(c.ID)
	if idx < 0 {
		return
	}
	evs, err := r.game.Play(game.Seat(idx), c.Cards)
	if err != nil {
		safeSend(r.seats[idx].Prog, protocol.ErrorMsg{Text: err.Error()})
		return
	}
	r.applyEvents(evs)
	// If the turn now rests on a disconnected seat, the upcoming auto-advance may
	// resolve the trick in this same step and clear the table. Show the play first so
	// clients can animate it before it vanishes.
	if r.phase == protocol.InGame && r.game.Table != nil && !r.seats[r.game.Turn].Connected {
		r.fanout()
	}
	r.afterTransition()
}

func (r *Room) handlePass(c PassCmd) {
	if r.phase != protocol.InGame {
		return
	}
	idx := r.seatIndexByID(c.ID)
	if idx < 0 {
		return
	}
	evs, err := r.game.Pass(game.Seat(idx))
	if err != nil {
		safeSend(r.seats[idx].Prog, protocol.ErrorMsg{Text: err.Error()})
		return
	}
	r.applyEvents(evs)
	r.afterTransition()
}

func (r *Room) handleNextHand(c NextHandCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Finished {
		return
	}
	r.startGame()
	r.afterTransition()
}

func (r *Room) handleLeave(id string) {
	idx := r.seatIndexByID(id)
	if idx < 0 {
		return
	}
	seat := r.seats[idx]
	if r.phase == protocol.Waiting {
		r.seats = append(r.seats[:idx], r.seats[idx+1:]...)
		if seat.Host {
			r.promoteHost() // the host left the lobby; hand off so the room stays startable
		}
		r.fanout()
		return
	}
	seat.Connected = false
	r.afterTransition()
}

// promoteHost grants host to the first remaining connected human seat when no seat
// holds it (e.g. the host left the waiting room). Bots never host. No-op if a host
// remains or none can be promoted.
func (r *Room) promoteHost() {
	for _, s := range r.seats {
		if s.Host {
			return
		}
	}
	for _, s := range r.seats {
		if s.Connected && !s.Bot {
			s.Host = true
			return
		}
	}
}

// afterTransition runs after any state change: fast-forward disconnected humans
// synchronously, schedule the bot the turn now rests on (if any), then fan out.
func (r *Room) afterTransition() {
	r.autoAdvanceForDisconnected()
	r.maybeScheduleBot()
	r.fanout()
}

// autoAdvanceForDisconnected keeps play moving on a *disconnected* seat's turn
// (bots are Connected, so they are never swept here — they use the delayed
// scheduler instead).
func (r *Room) autoAdvanceForDisconnected() {
	guard := 0
	for r.phase == protocol.InGame && !r.seats[r.game.Turn].Connected {
		guard++
		if guard > 500 {
			return
		}
		evs := r.forcedMove(r.game.Turn)
		if evs == nil {
			return
		}
		r.applyEvents(evs)
	}
}

// forcedMove applies a guaranteed-legal fallback for seat: pass while following,
// else lead its lowest card (the opener when it's the first play). nil on error.
func (r *Room) forcedMove(seat game.Seat) []game.Event {
	if r.game.Table == nil {
		hand := r.game.Hands[seat]
		if len(hand) == 0 {
			return nil
		}
		evs, err := r.game.Play(seat, []game.Card{hand[0]}) // hand is sorted ascending
		if err != nil {
			return nil
		}
		return evs
	}
	evs, err := r.game.Pass(seat)
	if err != nil {
		return nil
	}
	return evs
}

// maybeScheduleBot schedules a delayed move if the turn rests on a bot. The 1s
// wait happens in a spawned goroutine (never the actor); it Submits a BotActCmd
// back, tagged with turnToken so a stale timer is ignored.
func (r *Room) maybeScheduleBot() {
	if r.phase != protocol.InGame {
		return
	}
	seat := int(r.game.Turn)
	if !r.seats[seat].Bot {
		return
	}
	r.turnToken++
	tok := r.turnToken
	delay := r.botDelay
	go func() {
		time.Sleep(delay)
		r.Submit(BotActCmd{Seat: seat, Token: tok})
	}()
}

func (r *Room) handleBotAct(c BotActCmd) {
	if r.phase != protocol.InGame || c.Token != r.turnToken {
		return // stale timer or hand ended
	}
	if c.Seat < 0 || c.Seat >= len(r.seats) || !r.seats[c.Seat].Bot || int(r.game.Turn) != c.Seat {
		return
	}
	mv := bot.ChooseMove(r.game, game.Seat(c.Seat), r.seats[c.Seat].BotLevel, r.rng)
	var evs []game.Event
	var err error
	if mv.Pass {
		evs, err = r.game.Pass(game.Seat(c.Seat))
	} else {
		evs, err = r.game.Play(game.Seat(c.Seat), mv.Cards)
	}
	if err != nil {
		evs = r.forcedMove(game.Seat(c.Seat)) // never stall on a policy bug
		if evs == nil {
			return
		}
	}
	r.applyEvents(evs)
	r.afterTransition()
}

func (r *Room) handleSetLetter(c SetLetterCmd) {
	if r.phase != protocol.Waiting {
		return
	}
	s := r.seatByID(c.ID)
	if s == nil {
		return
	}
	L := upperByte(c.Letter)
	if L < 'A' || L > 'Z' || L == s.Letter {
		r.fanout() // invalid or unchanged: let the client snap back to the truth
		return
	}
	var holder *Seat
	for _, o := range r.seats {
		if o != s && o.Letter == L {
			holder = o
			break
		}
	}
	if holder != nil && !holder.Bot {
		r.fanout() // a human holds it: reject
		return
	}
	s.Letter = L
	if holder != nil { // a bot held it: humans win, bump the bot elsewhere
		holder.Letter = r.randomFreeLetter()
	}
	r.fanout()
}

func (r *Room) handleAddBot(c AddBotCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Waiting {
		return
	}
	if len(r.seats) >= r.maxSeats {
		safeSend(s.Prog, protocol.ErrorMsg{Text: "room is full"})
		return
	}
	level := c.Level
	if level < 1 {
		level = 5
	}
	if level > 9 {
		level = 9
	}
	r.seats = append(r.seats, &Seat{
		ID: NewID(), Connected: true, Bot: true, BotLevel: level, Letter: r.randomFreeLetter(),
	})
	r.fanout()
}

func (r *Room) handleRemoveBot(c RemoveBotCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Waiting {
		return
	}
	for i := len(r.seats) - 1; i >= 0; i-- {
		if r.seats[i].Bot {
			r.seats = append(r.seats[:i], r.seats[i+1:]...)
			r.fanout()
			return
		}
	}
}

// freeLetter is the lowest A-Z letter no seat holds (human default).
func (r *Room) freeLetter() byte {
	for L := byte('A'); L <= 'Z'; L++ {
		if !r.letterTaken(L) {
			return L
		}
	}
	return 'A' // unreachable: at most maxSeats <= 26 seats
}

// randomFreeLetter is a random A-Z letter no seat holds (bots get these).
func (r *Room) randomFreeLetter() byte {
	var free []byte
	for L := byte('A'); L <= 'Z'; L++ {
		if !r.letterTaken(L) {
			free = append(free, L)
		}
	}
	if len(free) == 0 {
		return 'A'
	}
	return free[r.rng.Intn(len(free))]
}

func (r *Room) letterTaken(L byte) bool {
	for _, s := range r.seats {
		if s.Letter == L {
			return true
		}
	}
	return false
}

func upperByte(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

func (r *Room) applyEvents(evs []game.Event) {
	// The table view carries all live state; only the end-of-hand event needs
	// handling here.
	for _, e := range evs {
		if _, ok := e.(game.GameWonEvent); ok {
			r.handleGameWon()
		}
	}
}

func (r *Room) handleGameWon() {
	scores := r.game.HandScores()
	for i := range r.seats {
		r.seats[i].Score += scores[i]
	}
	r.phase = protocol.Finished
}

// fanout pushes a per-viewer redacted snapshot to every connected seat, bumping
// rev so clients can drop out-of-order sends.
func (r *Room) fanout() {
	r.rev++
	for i, s := range r.seats {
		if !s.Connected || s.Prog == nil {
			continue
		}
		safeSend(s.Prog, protocol.StateSnapshotMsg{Snap: r.snapshotFor(i)})
	}
}

// snapshotFor builds the redacted view for viewer. Redaction choke point:
// opponents' cards never leave here, only counts.
func (r *Room) snapshotFor(viewer int) protocol.StateSnapshot {
	players := make([]protocol.PlayerView, len(r.seats))
	for i, s := range r.seats {
		pv := protocol.PlayerView{
			Seat:      i,
			Letter:    s.Letter,
			Connected: s.Connected,
			IsYou:     i == viewer,
			IsHost:    s.Host,
			IsBot:     s.Bot,
			BotLevel:  s.BotLevel,
			Score:     s.Score,
		}
		if r.game != nil {
			pv.CardCount = len(r.game.Hands[i])
			pv.IsTurn = r.phase == protocol.InGame && int(r.game.Turn) == i
			// passing is locked out, so this stays set for the whole trick
			pv.Passed = r.phase == protocol.InGame && r.game.Passed[i]
		}
		players[i] = pv
	}
	snap := protocol.StateSnapshot{
		Phase:    r.phase,
		Rev:      r.rev,
		YouSeat:  viewer,
		IsHost:   r.seats[viewer].Host,
		MaxSeats: r.maxSeats,
		MinStart: r.minStart,
		Players:  players,
		Turn:     -1,
		TableBy:  -1,
		Winner:   -1,
	}
	if r.game != nil {
		snap.YourHand = append([]game.Card(nil), r.game.Hands[viewer]...)
		if r.game.Table != nil {
			snap.Table = append([]game.Card(nil), r.game.Table.Cards...)
			snap.TableBy = int(r.game.Leader) // Leader owns the current Table combo
		}
		snap.Turn = int(r.game.Turn)
		snap.Winner = int(r.game.Winner)
	}
	return snap
}

func (r *Room) seatByID(id string) *Seat {
	for _, s := range r.seats {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (r *Room) seatIndexByID(id string) int {
	for i, s := range r.seats {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// safeSend sends msg from a fresh goroutine, tolerating a torn-down program.
func safeSend(p *tea.Program, msg tea.Msg) {
	if p == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		p.Send(msg)
	}()
}

func safeSendAll(seats []*Seat, msg tea.Msg) {
	for _, s := range seats {
		if s.Connected {
			safeSend(s.Prog, msg)
		}
	}
}
