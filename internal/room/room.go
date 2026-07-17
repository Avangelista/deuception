// Package room hosts a single, in-memory Big 2 room. One actor goroutine owns
// all mutable state; sessions submit Commands and get per-viewer redacted
// snapshots. The live game is in-memory only; preferences (house rules, reaction
// labels, and remembered letters) are saved through an optional Persister.
package room

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Avangelista/big2-tui/internal/bot"
	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
)

// Seat is one player position in the room. A bot seat is Connected with Bot set
// and a nil Prog, so it is skipped by fanout and never swept as a dropout.
type Seat struct {
	ID        string
	Identity  string // stable cross-session identity (SSH key fingerprint, or LocalIdentity); "" if none
	Prog      *tea.Program
	Connected bool
	Host      bool
	Bot       bool
	Letter    byte // chosen display letter, unique per room
	Score     int  // cumulative penalty across hands (lower is better)
}

// LocalIdentity is the fixed identity of the local host player, who has no SSH key.
const LocalIdentity = "local"

// Persister saves the room's preferences whenever they change. A nil persister (the
// default) disables persistence, which the tests rely on.
type Persister interface {
	Save(rules game.Rules, reactions []string, letters map[string]string)
}

// Option configures a Room at construction, before its actor goroutine starts.
type Option func(*Room)

// WithPersister makes the room save its preferences on every change.
func WithPersister(p Persister) Option { return func(r *Room) { r.persister = p } }

// WithSavedPrefs seeds a room from persisted preferences: the ruleset (sanitised), the
// reaction labels (only if the expected count), and the remembered letters.
func WithSavedPrefs(rules game.Rules, reactions []string, letters map[string]string) Option {
	return func(r *Room) {
		r.rules = rules.Sanitized()
		if len(reactions) == len(protocol.Emotes) {
			r.reactions = sanitizeReactions(reactions)
		}
		for k, v := range letters {
			r.letters[k] = v
		}
	}
}

// sanitizeReactions keeps each valid label and falls back to the default for any that is
// blank or too long, so a hand-edited prefs file can't break rendering.
func sanitizeReactions(in []string) []string {
	out := protocol.DefaultReactions()
	for i := range out {
		s := strings.TrimSpace(in[i])
		if s != "" && utf8.RuneCountInString(s) <= protocol.MaxReactionLen {
			out[i] = s
		}
	}
	return out
}

// Room is a single game room served to many connections.
type Room struct {
	cmds       chan Command
	maxSeats   int
	minStart   int
	rng        *mrand.Rand
	botDelay   time.Duration // how long a bot "thinks" before acting
	trickDelay time.Duration // how long a won trick is held on screen before it clears

	// actor-owned state (only touched inside run):
	seats        []*Seat
	game         *game.GameState
	phase        protocol.Phase
	rules        game.Rules        // host-configured house rules; applied to each hand
	reactions    []string          // room-wide reaction labels (host-configurable)
	letters      map[string]string // identity -> last picked letter, restored on rejoin
	persister    Persister         // saves prefs on change; nil disables persistence
	lastWinnerID string            // winner of the previous hand, for the winner-leads rule
	rev          int               // monotonic snapshot revision; lets clients drop out-of-order sends
	turnToken    int               // bumped whenever a bot is scheduled; invalidates stale timers
	trickToken   int               // bumped whenever a trick hold is scheduled; invalidates stale timers
	trickReveal  *trickReveal      // set while a won trick is held on screen before it clears
}

// trickReveal is the just-completed trick, held briefly so the winning card and the
// final pass are visible before the next trick starts. snapshotFor shows this in
// place of the (already reset) live state while it is set.
type trickReveal struct {
	table  []game.Card
	by     int    // seat that won the trick (owns the table combo)
	passed []bool // who passed, including the final pass that ended the trick
}

// New starts a room actor. maxSeats caps the table, minStart is the fewest that can
// start. Options seed persisted preferences and a persister before the actor runs.
func New(maxSeats, minStart int, rng *mrand.Rand, opts ...Option) *Room {
	r := &Room{
		cmds:       make(chan Command, 64),
		maxSeats:   maxSeats,
		minStart:   minStart,
		rng:        rng,
		phase:      protocol.Waiting,
		reactions:  protocol.DefaultReactions(),
		letters:    map[string]string{},
		botDelay:   time.Second,
		trickDelay: protocol.RevealHold,
	}
	for _, o := range opts {
		o(r)
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
		case trickResetCmd:
			r.handleTrickReset(cmd)
		case DisconnectCmd:
			r.handleLeave(cmd.ID)
		case QuitCmd:
			r.handleLeave(cmd.ID)
		case EmoteCmd:
			r.handleEmote(cmd)
		case SetRulesCmd:
			r.handleSetRules(cmd)
		case SetReactionCmd:
			r.handleSetReaction(cmd)
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

// trickResetCmd fires after the trick-won hold; token guards against a stale timer.
type trickResetCmd struct{ token int }

func (trickResetCmd) isCmd() {}

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
	seat := &Seat{ID: c.ID, Identity: c.Identity, Prog: c.Prog, Connected: true, Host: isHost, Letter: r.rememberedLetter(c.Identity)}
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
	r.game = game.NewGame(len(r.seats), r.rules)
	if err := r.game.Deal(r.rng); err != nil {
		safeSendAll(r.seats, protocol.ErrorMsg{Text: "failed to deal: " + err.Error()})
		return
	}
	// Winner-leads: the previous hand's winner opens the next one freely. The first
	// hand of a match (no prior winner) always opens on the 3D.
	if r.rules.Lead == game.LeadWinner {
		if idx := r.seatIndexByID(r.lastWinnerID); idx >= 0 {
			r.game.LeadFrom(game.Seat(idx))
		}
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
	r.trickReveal = nil // a fresh play ends any pending trick-won hold
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
	// Capture the trick before the pass, in case it wins the trick and the engine
	// clears the table and pass flags in the same step.
	table, passed := r.game.Table, append([]bool(nil), r.game.Passed...)
	evs, err := r.game.Pass(game.Seat(idx))
	if err != nil {
		safeSend(r.seats[idx].Prog, protocol.ErrorMsg{Text: err.Error()})
		return
	}
	r.applyEvents(evs)
	if r.beginTrickReveal(evs, table, passed, idx) {
		return
	}
	r.afterTransition()
}

// beginTrickReveal holds a just-won trick on screen for a beat, so the winning card
// and the final pass are visible before the next trick starts. It returns true (and
// the caller then skips afterTransition) when a reveal was started. table/passed are
// the trick captured before the pass reset them; passer is the seat that passed.
func (r *Room) beginTrickReveal(evs []game.Event, table *game.Combo, passed []bool, passer int) bool {
	if r.phase != protocol.InGame || r.trickDelay <= 0 || table == nil || !wonTrick(evs) {
		return false
	}
	if passer >= 0 && passer < len(passed) {
		passed[passer] = true
	}
	r.trickReveal = &trickReveal{
		table:  append([]game.Card(nil), table.Cards...),
		by:     int(r.game.Leader), // the leader took the trick and owns the table combo
		passed: passed,
	}
	r.trickToken++
	tok, delay := r.trickToken, r.trickDelay
	go func() {
		time.Sleep(delay)
		r.Submit(trickResetCmd{token: tok})
	}()
	r.fanout()
	return true
}

// wonTrick reports whether evs contains a TrickWonEvent.
func wonTrick(evs []game.Event) bool {
	for _, e := range evs {
		if _, ok := e.(game.TrickWonEvent); ok {
			return true
		}
	}
	return false
}

// handleTrickReset ends a trick-won hold and resumes normal play.
func (r *Room) handleTrickReset(c trickResetCmd) {
	if c.token != r.trickToken || r.trickReveal == nil {
		return // superseded by a newer play/hold, or already cleared
	}
	r.trickReveal = nil
	r.afterTransition()
}

func (r *Room) handleNextHand(c NextHandCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Finished {
		return
	}
	// Not enough players left to deal another hand: the host can only quit.
	if r.connectedCount() < r.minStart {
		return
	}
	// Drop everyone who left so the next hand starts clean, rather than carrying them as
	// permanent gone-markers.
	r.dropDisconnected()
	r.startGame()
	r.afterTransition()
}

// connectedCount is how many seats are still in the game (humans and bots).
func (r *Room) connectedCount() int {
	n := 0
	for _, s := range r.seats {
		if s.Connected {
			n++
		}
	}
	return n
}

// dropDisconnected removes every seat whose player has left, then makes sure a
// connected human still holds the host. Call only when enough seats remain to play.
func (r *Room) dropDisconnected() {
	kept := r.seats[:0]
	for _, s := range r.seats {
		if s.Connected {
			kept = append(kept, s)
		}
	}
	r.seats = kept
	r.promoteHost()
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
	if seat.Host { // the host left mid-game/end: hand off so someone can still advance or quit
		seat.Host = false
		r.promoteHost()
	}
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
	if r.autoAdvanceForDisconnected() {
		return // a trick-won hold was started; trickReset will resume play
	}
	r.maybeScheduleBot()
	r.fanout()
}

// autoAdvanceForDisconnected keeps play moving on a *disconnected* seat's turn
// (bots are Connected, so they are never swept here — they use the delayed
// scheduler instead).
// autoAdvanceForDisconnected keeps play moving on a disconnected seat's turn. It
// returns true if a forced pass won a trick and a trick-won hold was started, so the
// caller skips its own fanout (the trickReset resumes play a beat later).
func (r *Room) autoAdvanceForDisconnected() bool {
	guard := 0
	for r.phase == protocol.InGame && !r.seats[r.game.Turn].Connected {
		guard++
		if guard > 500 {
			return false
		}
		table, passed, seat := r.game.Table, append([]bool(nil), r.game.Passed...), int(r.game.Turn)
		evs := r.forcedMove(r.game.Turn)
		if evs == nil {
			return false
		}
		r.applyEvents(evs)
		if r.beginTrickReveal(evs, table, passed, seat) {
			return true
		}
	}
	return false
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
	mv := bot.ChooseMove(r.game, game.Seat(c.Seat))
	table, passed := r.game.Table, append([]bool(nil), r.game.Passed...)
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
	if mv.Pass && err == nil && r.beginTrickReveal(evs, table, passed, c.Seat) {
		return
	}
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
	if L < 'A' || L > 'Z' {
		r.fanout() // invalid: let the client snap back to the truth
		return
	}
	if L == s.Letter {
		// No change, but an explicit pick of the current (maybe auto-assigned) letter
		// still counts, so it is remembered for next session.
		r.rememberLetter(s)
		r.fanout()
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
	r.rememberLetter(s)
	r.fanout()
}

// rememberLetter persists a seat's current letter under its identity for next session.
// No-op for an anonymous seat or when that letter is already saved (avoids a redundant
// write).
func (r *Room) rememberLetter(s *Seat) {
	if s.Identity == "" || r.letters[s.Identity] == string(s.Letter) {
		return
	}
	r.letters[s.Identity] = string(s.Letter)
	r.persist()
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
	r.seats = append(r.seats, &Seat{
		ID: NewID(), Connected: true, Bot: true, Letter: r.randomFreeLetter(),
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

// randomFreeLetter is a random A-Z letter no seat holds; new joiners and bots both
// get one so nobody's default is predictable.
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
	if w := int(r.game.Winner); w >= 0 && w < len(r.seats) {
		r.lastWinnerID = r.seats[w].ID // remembered for the winner-leads rule next hand
	}
	r.phase = protocol.Finished
}

// fanout pushes a per-viewer redacted snapshot to every connected seat, bumping
// rev so clients can drop out-of-order sends.
// handleEmote broadcasts a quick-chat reaction to every session; a fresh reaction from
// a player replaces their previous one. Reactions are public, so no redaction is involved.
func (r *Room) handleEmote(c EmoteCmd) {
	if r.phase != protocol.InGame && r.phase != protocol.Finished {
		return // reactions live during a hand and on the score screen
	}
	idx := r.seatIndexByID(c.ID)
	if idx < 0 || c.Code < 0 || c.Code >= len(r.reactions) {
		return
	}
	safeSendAll(r.seats, protocol.EmoteMsg{Seat: idx, Code: c.Code})
}

// handleSetRules replaces the house ruleset (host only, waiting room). Locked once a
// game is in progress so the rules can't shift mid-match.
func (r *Room) handleSetRules(c SetRulesCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Waiting {
		return
	}
	r.rules = c.Rules.Sanitized() // never trust a client to send an in-range enum
	r.persist()
	r.fanout()
}

// handleSetReaction sets one reaction label room-wide (host only, waiting room). An
// empty or too-long label is rejected; the client is snapped back to the truth.
func (r *Room) handleSetReaction(c SetReactionCmd) {
	s := r.seatByID(c.ID)
	if s == nil || !s.Host || r.phase != protocol.Waiting {
		return
	}
	if c.Index < 0 || c.Index >= len(r.reactions) {
		return
	}
	text := strings.TrimSpace(c.Text)
	if text == "" || utf8.RuneCountInString(text) > protocol.MaxReactionLen {
		r.fanout()
		return
	}
	r.reactions[c.Index] = text
	r.persist()
	r.fanout()
}

// rememberedLetter returns identity's saved letter when it can be claimed (free, or held
// only by a bot that gets bumped), otherwise a random free one. Empty identities are
// never remembered.
func (r *Room) rememberedLetter(identity string) byte {
	if identity != "" {
		if s, ok := r.letters[identity]; ok && len(s) == 1 {
			if L := upperByte(s[0]); L >= 'A' && L <= 'Z' && r.claimLetter(L) {
				return L
			}
		}
	}
	return r.randomFreeLetter()
}

// claimLetter reports whether L is available for a joining seat, bumping a bot that
// holds it (humans outrank bots, as in handleSetLetter). A human holder blocks the claim.
// Call before the new seat is appended, so it can't conflict with itself.
func (r *Room) claimLetter(L byte) bool {
	for _, s := range r.seats {
		if s.Letter != L {
			continue
		}
		if s.Bot {
			s.Letter = r.randomFreeLetter() // bump the bot elsewhere
			return true
		}
		return false // a human holds it
	}
	return true // free
}

// persist snapshots the room's preferences and hands them to the persister (if any) to
// save. Called on the actor goroutine after any pref change.
func (r *Room) persist() {
	if r.persister == nil {
		return
	}
	letters := make(map[string]string, len(r.letters))
	for k, v := range r.letters {
		letters[k] = v
	}
	r.persister.Save(r.rules, append([]string(nil), r.reactions...), letters)
}

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
			Score:     s.Score,
		}
		if r.game != nil {
			pv.CardCount = len(r.game.Hands[i])
			if r.trickReveal != nil {
				// Held won trick: show its final pass flags, no active turn.
				pv.Passed = i < len(r.trickReveal.passed) && r.trickReveal.passed[i]
			} else {
				pv.IsTurn = r.phase == protocol.InGame && int(r.game.Turn) == i
				// passing is locked out, so this stays set for the whole trick
				pv.Passed = r.phase == protocol.InGame && r.game.Passed[i]
			}
		}
		players[i] = pv
	}
	snap := protocol.StateSnapshot{
		Phase:     r.phase,
		Rev:       r.rev,
		YouSeat:   viewer,
		IsHost:    r.seats[viewer].Host,
		MaxSeats:  r.maxSeats,
		MinStart:  r.minStart,
		Players:   players,
		Turn:      -1,
		TableBy:   -1,
		Winner:    -1,
		Rules:     r.rules,
		Reactions: append([]string(nil), r.reactions...),
	}
	if r.game != nil {
		snap.YourHand = append([]game.Card(nil), r.game.Hands[viewer]...)
		if r.trickReveal != nil {
			// Held won trick: show the completed trick with no active turn until it clears.
			snap.Table = append([]game.Card(nil), r.trickReveal.table...)
			snap.TableBy = r.trickReveal.by
			snap.Turn = -1
		} else {
			if r.game.Table != nil {
				snap.Table = append([]game.Card(nil), r.game.Table.Cards...)
				snap.TableBy = int(r.game.Leader) // Leader owns the current Table combo
			}
			snap.Turn = int(r.game.Turn)
			// The opening play (must include OpenCard) is only your concern on your
			// first-of-the-game lead.
			if r.phase == protocol.InGame && r.game.FirstPlay() && int(r.game.Turn) == viewer {
				snap.Opening = true
				snap.OpenCard = r.game.OpenCard
			}
		}
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
