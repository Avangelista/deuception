// Package tui is the Bubble Tea client for a Big 2 session: one Model per
// connection, rendering the room's per-viewer snapshots and submitting actions.
package tui

import (
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

// commander is the subset of *room.Room the TUI needs (submit actions).
type commander interface {
	Submit(room.Command)
}

type quitMsg struct{}

// Model is a single connection's view state.
type Model struct {
	room     commander
	id       string
	joinHint string // "ssh -p PORT IP" shown in the waiting room
	prog     *tea.Program

	r          *lipgloss.Renderer
	st         styles
	asciiSuits bool // draw suit letters (D/C/H/S) instead of pips - see resolveASCIISuits

	w, h int
	snap *protocol.StateSnapshot

	cursor     int
	scroll     int // off-turn view offset (leftmost visible card); no cursor is shown
	selected   map[int]bool
	playable   []bool // on your turn, which hand cards can still complete a legal play
	wasMyTurn  bool   // whether your turn was active last refresh; a false->true edge resets the cursor
	sortBySuit bool   // sort the hand by suit instead of by rank
	hint       string
	hintGen    int  // bumped on each hint so a stale timer can't clear a newer hint
	lastRev    int  // highest snapshot revision applied; drops out-of-order deliveries
	boss       bool // hide the card UI (blank the borders so the board reads as plain text)
	kicked     string

	pendingBotLevel int // difficulty applied to the next added bot (1-9)

	reacting    bool               // the quick-chat picker is open (footer shows the presets)
	confirmQuit bool               // esc in-game asks first; enter confirms, esc cancels
	emotes      map[int]emoteState // active reaction per absolute seat, flashed beside its label
	emoteGen    int                // bumped per reaction so a stale timer can't clear a newer one

	pileCur    []game.Card    // the play currently shown in the pile
	pilePrev   []game.Card    // the play it beat, drawn under the slide (same size within a trick)
	pileDir    [2]int         // unit direction the current play slides in from
	pileStep   int            // slide frame, 0 (at the side) .. pileSteps (centred/at rest)
	pileGen    int            // invalidates stale slide ticks
	pileFinish pileFinishMode // what to do once the slide and its hold settle
}

type hintExpireMsg struct{ gen int }

// emoteState is a reaction currently shown for a seat; gen lets its expiry timer skip a
// reaction that a newer one already replaced.
type emoteState struct{ code, gen int }

// emoteExpireMsg clears a seat's reaction once its beat elapses.
type emoteExpireMsg struct{ seat, gen int }

// emoteHold is how long a reaction stays on screen before it fades.
const emoteHold = 2 * time.Second

// New builds a Model; renderer must be session-scoped (MakeRenderer for SSH).
func New(r commander, id, joinHint string, renderer *lipgloss.Renderer) *Model {
	return &Model{
		room:            r,
		id:              id,
		joinHint:        joinHint,
		r:               renderer,
		st:              newStyles(renderer),
		asciiSuits:      resolveASCIISuits(),
		selected:        map[int]bool{},
		emotes:          map[int]emoteState{},
		pendingBotLevel: 5,
	}
}

// resolveASCIISuits decides whether cards show suit letters (D/C/H/S) instead of
// pips (♦♣♥♠). Pips are the default ("go all in on glyphs"), but they measure as
// width-1 only on terminals that honour text presentation; DEUCE_SUITS lets an
// operator pin the mode, and a CJK/ambiguous-width locale auto-falls back to letters
// (there the pips render double-width and would shear the fixed grid).
//
//	DEUCE_SUITS=glyph  always pips        =ascii  always letters
//	DEUCE_SUITS=auto   pips, letters under a CJK locale   (unset: same as auto)
func resolveASCIISuits() bool {
	switch strings.ToLower(os.Getenv("DEUCE_SUITS")) {
	case "ascii":
		return true
	case "glyph":
		return false
	default: // "auto" or unset
		return cjkLocale()
	}
}

// cjkLocale reports whether the process locale is one where ambiguous-width glyphs
// (the suit pips among them) are drawn double-width.
func cjkLocale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		l := strings.ToLower(os.Getenv(k))
		if strings.Contains(l, "zh") || strings.Contains(l, "ja") || strings.Contains(l, "ko") {
			return true
		}
	}
	return false
}

// suitRune is the display rune for a suit: a pip, or its ASCII letter in ascii mode.
func (m *Model) suitRune(s game.Suit) rune {
	if m.asciiSuits {
		return rune(s.String()[0])
	}
	return s.Glyph()
}

// suitInfo classifies a composited rune: whether it is a suit cell (pip or letter)
// and, if so, whether it is a red suit. Rank and box-drawing runes never collide
// with suit runes, so pile colouring is a pure function of the rune.
func (m *Model) suitInfo(r rune) (red, isSuit bool) {
	if m.asciiSuits {
		switch r {
		case 'D', 'H':
			return true, true
		case 'C', 'S':
			return false, true
		}
		return false, false
	}
	switch r {
	case '♦', '♥':
		return true, true
	case '♣', '♠':
		return false, true
	}
	return false, false
}

// SetProgram records the program so the room can push updates.
func (m *Model) SetProgram(p *tea.Program) { m.prog = p }

func (m *Model) Init() tea.Cmd { return nil }

// Update handles input and pushed room messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clampScroll() // resize changed how many cards fit; keep scroll in range
	case protocol.StateSnapshotMsg:
		return m, m.applySnapshot(msg.Snap)
	case pileAnimMsg:
		return m, m.advancePile(msg)
	case pileFinishMsg:
		return m, m.finishPile(msg)
	case protocol.ErrorMsg:
		return m, m.setHint(msg.Text)
	case hintExpireMsg:
		if msg.gen == m.hintGen {
			m.hint = ""
		}
	case protocol.EmoteMsg:
		return m, m.showEmote(msg.Seat, msg.Code)
	case emoteExpireMsg:
		if e, ok := m.emotes[msg.seat]; ok && e.gen == msg.gen {
			delete(m.emotes, msg.seat)
		}
	case protocol.KickedMsg:
		m.kicked = msg.Reason
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return quitMsg{} })
	case protocol.RoomClosedMsg:
		m.kicked = "room closed"
		return m, tea.Quit
	case quitMsg:
		return m, tea.Quit
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applySnapshot(s protocol.StateSnapshot) tea.Cmd {
	// Snapshots arrive on their own goroutines, so a later fanout can land first;
	// ignore anything older than the newest applied.
	if s.Rev != 0 && s.Rev < m.lastRev {
		return nil
	}
	m.lastRev = s.Rev
	var prevHand []game.Card
	if m.snap != nil {
		prevHand = m.snap.YourHand
	}
	m.snap = &s
	// Reset selection/hint/scroll when the hand's contents change; keying on size
	// alone would miss an equal-size redeal and carry stale indices into it.
	if !sameHand(prevHand, s.YourHand) {
		m.selected = map[int]bool{}
		m.hint = ""
		m.scroll = 0
	}
	// Clear a stale "not your turn" once it's actually your turn.
	if s.Phase == protocol.InGame && s.Turn == s.YouSeat {
		m.hint = ""
	}
	cmd := m.updatePile(s)
	if m.cursor >= len(s.YourHand) {
		m.cursor = max(0, len(s.YourHand)-1)
	}
	m.clampScroll()
	m.recomputePlayable()
	return cmd
}

// sameHand reports whether two hands hold the same cards in the same order.
func sameHand(a, b []game.Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pileAnimMsg advances the play-in slide one frame; pileFinishMsg fires when the
// centred hold ends. gen drops ticks from a slide superseded by a newer play.
type pileAnimMsg struct{ gen int }
type pileFinishMsg struct{ gen int }

// pileSteps and pileTickEvery time the play-in slide (a short glide from the
// player's side to the centre); pileHold is the beat the card rests centred before
// the pile clears or the scoreboard takes over - the shared reveal beat.
const (
	pileSteps     = 8
	pileTickEvery = 22 * time.Millisecond
	pileHold      = protocol.RevealHold
)

// pileFinishMode is what happens once a slide and its hold complete.
type pileFinishMode uint8

const (
	finishNone  pileFinishMode = iota // stay put until the next play
	finishScore                       // hand over: reveal the winning card, then the scoreboard
	finishClear                       // trick reset mid-slide: let the card land, then clear it
)

// updatePile reacts to a snapshot: a new table combo starts a slide from the side
// of the player who made it, opaquely covering the play it beat (same size within a
// trick). A finished hand keeps its winning play so it can slide in before the
// scoreboard. When the table clears while a slide is still running (a trick won the
// instant it was played - e.g. disconnected opponents auto-passing), the card is
// allowed to finish sliding in before the pile clears.
func (m *Model) updatePile(s protocol.StateSnapshot) tea.Cmd {
	if len(s.Table) == 0 || s.Phase == protocol.Waiting {
		if len(m.pileCur) > 0 && m.pileStep < pileSteps {
			m.pileFinish = finishClear // finish the in-flight slide, then clear
			return nil
		}
		m.clearPile()
		return nil
	}
	if sameHand(m.pileCur, s.Table) {
		return nil // same play still on the table
	}
	prev := m.pileCur
	m.pileCur = append([]game.Card(nil), s.Table...)
	// Cover the beaten play only when it is the same size (guaranteed within a
	// trick). A size change means a new trick, so there is nothing to cover.
	m.pilePrev = nil
	if len(prev) == len(m.pileCur) {
		m.pilePrev = prev
	}
	dx, dy := 0, 0
	if s.TableBy >= 0 {
		n := len(s.Players)
		dx, dy = pileNudge((s.TableBy-s.YouSeat+n)%n, n)
	}
	m.pileDir = [2]int{dx, dy}
	m.pileGen++
	m.pileFinish = finishNone
	if s.Phase == protocol.Finished {
		m.pileFinish = finishScore // the winning play: hold, then the scoreboard
	}
	if dx == 0 && dy == 0 { // no direction: skip straight to centred
		m.pileStep, m.pilePrev = pileSteps, nil
		if m.pileFinish != finishNone {
			return m.pileHoldTick()
		}
		return nil
	}
	m.pileStep = 0
	return m.pileTick()
}

// clearPile resets the pile to empty.
func (m *Model) clearPile() {
	m.pileCur, m.pilePrev, m.pileDir, m.pileStep, m.pileFinish = nil, nil, [2]int{}, 0, finishNone
}

// pileTick schedules the next slide frame, tagged with the current generation.
func (m *Model) pileTick() tea.Cmd {
	gen := m.pileGen
	return tea.Tick(pileTickEvery, func(time.Time) tea.Msg { return pileAnimMsg{gen: gen} })
}

// pileHoldTick schedules the end of the centred hold, after which the pile finishes.
func (m *Model) pileHoldTick() tea.Cmd {
	gen := m.pileGen
	return tea.Tick(pileHold, func(time.Time) tea.Msg { return pileFinishMsg{gen: gen} })
}

// advancePile steps the slide; once it settles centred it drops the covered play and
// either stops or, if a finish is pending, starts the centred hold.
func (m *Model) advancePile(msg pileAnimMsg) tea.Cmd {
	if msg.gen != m.pileGen || m.pileStep >= pileSteps {
		return nil
	}
	m.pileStep++
	if m.pileStep >= pileSteps {
		m.pilePrev = nil      // fully covered now: only the current play remains
		m.recomputePlayable() // the slide landed; your turn (and its playable set) activates
		if m.pileFinish != finishNone {
			return m.pileHoldTick()
		}
		return nil
	}
	return m.pileTick()
}

// finishPile runs when the centred hold elapses: clear a won trick's pile, or drop
// the win reveal so the scoreboard shows.
func (m *Model) finishPile(msg pileFinishMsg) tea.Cmd {
	if msg.gen != m.pileGen {
		return nil // superseded by a newer play
	}
	switch m.pileFinish {
	case finishClear:
		m.clearPile()
	case finishScore:
		m.pileFinish = finishNone
	}
	return nil
}

// SettlePile fast-forwards any in-flight slide to its resting centred frame. Used by
// the headless preview and tests, which don't run the tick loop.
func (m *Model) SettlePile() {
	m.pileStep, m.pilePrev, m.pileFinish = pileSteps, nil, finishNone
	m.recomputePlayable()
}

// clampScroll keeps the off-turn scroll within [0, len-maxHandCells] so a resize
// can't strand it past the end.
func (m *Model) clampScroll() {
	if m.snap == nil {
		return
	}
	maxScroll := len(m.snap.YourHand) - m.maxHandCells()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" { // hard quit, no confirmation
		m.room.Submit(room.QuitCmd{ID: m.id})
		return m, tea.Quit
	}
	// Quit confirmation (raised by esc in-game): enter confirms, esc cancels, the rest
	// is swallowed so the prompt stays put.
	if m.confirmQuit {
		switch k.String() {
		case "enter":
			m.room.Submit(room.QuitCmd{ID: m.id})
			return m, tea.Quit
		case "esc":
			m.confirmQuit = false
		}
		return m, nil
	}
	if k.String() == "esc" {
		switch {
		case m.reacting: // esc first dismisses the quick-chat picker
			m.reacting = false
		case m.snap != nil && m.snap.Phase == protocol.InGame:
			m.confirmQuit = true // ask before abandoning a game in progress
		default:
			m.room.Submit(room.QuitCmd{ID: m.id})
			return m, tea.Quit
		}
		return m, nil
	}
	if m.kicked != "" {
		return m, tea.Quit
	}
	if m.snap == nil {
		return m, nil
	}
	switch m.snap.Phase {
	case protocol.Waiting:
		return m.keyWaiting(k)
	case protocol.InGame:
		return m.keyGame(k)
	case protocol.Finished:
		return m.keyOver(k)
	}
	return m, nil
}

func (m *Model) keyWaiting(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	switch {
	case key == "enter":
		if m.snap.IsHost {
			m.room.Submit(room.StartCmd{ID: m.id})
		}
	case key == "+" || key == "=": // '=' is the unshifted '+' key
		if m.snap.IsHost {
			m.room.Submit(room.AddBotCmd{ID: m.id, Level: m.pendingBotLevel})
		}
	case key == "-":
		if m.snap.IsHost {
			m.room.Submit(room.RemoveBotCmd{ID: m.id})
		}
	case len(key) == 1 && key[0] >= '1' && key[0] <= '9':
		if m.snap.IsHost {
			m.pendingBotLevel = int(key[0] - '0')
		}
	case len(key) == 1 && isLetter(key[0]):
		m.room.Submit(room.SetLetterCmd{ID: m.id, Letter: key[0]}) // server enforces uniqueness
	}
	return m, nil
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// reactKey sends a quick-chat reaction if k is a digit in the preset range (works on or
// off turn, with or without the picker open) and reports whether it handled the key.
func (m *Model) reactKey(k tea.KeyMsg) bool {
	s := k.String()
	if len(s) != 1 || s[0] < '1' || int(s[0]-'0') > len(protocol.Emotes) {
		return false
	}
	m.room.Submit(room.EmoteCmd{ID: m.id, Code: int(s[0] - '1')})
	// The picker stays open after sending, so you can fire several; esc or r closes it.
	return true
}

func (m *Model) keyGame(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.reactKey(k) { // 1-5 fire a reaction any time, picker open or not
		return m, nil
	}
	hand := m.hand()
	myTurn := m.isMyTurn()
	switch k.String() {
	case "r":
		m.reacting = !m.reacting // toggle the quick-chat picker (a reminder; digits work regardless)
	case "left":
		// On turn move the cursor to the next playable card; off turn scroll the view.
		if myTurn {
			m.cursor = m.stepCursor(-1)
		} else if m.scroll > 0 {
			m.scroll--
		}
	case "right":
		if myTurn {
			m.cursor = m.stepCursor(1)
		} else if m.scroll < len(hand)-m.maxHandCells() {
			m.scroll++
		}
	case " ":
		if !myTurn || len(hand) == 0 {
			return m, nil
		}
		switch {
		case m.selected[m.cursor]:
			delete(m.selected, m.cursor)
			m.hint = ""
		case !m.cardPlayable(m.cursor):
			return m, nil // greyed: unplayable, can't be selected
		case len(m.selected) < 5:
			m.selected[m.cursor] = true // combos are at most 5 cards
			m.hint = ""
		default:
			return m, m.setHint("select up to 5 cards")
		}
		m.recomputePlayable() // the selection narrowed/widened what's playable
	case "c":
		m.selected = map[int]bool{}
		m.hint = ""
		m.recomputePlayable()
	case "enter":
		if !myTurn {
			return m, nil
		}
		cards := m.selectedCards()
		if len(cards) == 0 && m.cardPlayable(m.cursor) {
			cards = []game.Card{hand[m.cursor]} // quick-play the card under the cursor
		}
		if len(cards) == 0 {
			return m, nil
		}
		m.room.Submit(room.PlayCmd{ID: m.id, Cards: cards})
	case "x":
		if !myTurn {
			return m, nil
		}
		m.selected = map[int]bool{} // passing discards any pending selection
		m.room.Submit(room.PassCmd{ID: m.id})
	case "s", "S":
		m.toggleSort() // reorder the hand by rank or by suit
	case "h":
		m.boss = !m.boss // secret: hide the card UI (undocumented)
	}
	return m, nil
}

// isMyTurn reports whether the game is live and it is this viewer's turn - and the
// last play has finished sliding in, so the hand lifts only once the card lands.
func (m *Model) isMyTurn() bool {
	return m.snap != nil && m.snap.Phase == protocol.InGame &&
		m.snap.Turn == m.snap.YouSeat && !m.midPlaySlide()
}

// midPlaySlide reports whether a played card is still sliding into the pile. While
// it slides, the player it passed the turn to is not yet activated: their hand or
// fan stays down and lifts only once the card lands (the player who played dropped
// immediately, since the turn already moved off them).
func (m *Model) midPlaySlide() bool {
	return m.pileFinish == finishNone && len(m.pileCur) > 0 && m.pileStep < pileSteps
}

// showEmote flashes a seat's reaction, cleared after emoteHold unless a newer reaction
// from the same seat replaces it first.
func (m *Model) showEmote(seat, code int) tea.Cmd {
	m.emoteGen++
	gen := m.emoteGen
	m.emotes[seat] = emoteState{code: code, gen: gen}
	return tea.Tick(emoteHold, func(time.Time) tea.Msg { return emoteExpireMsg{seat: seat, gen: gen} })
}

// setHint shows a transient hint, cleared after a few seconds unless a newer one
// replaces it first.
func (m *Model) setHint(text string) tea.Cmd {
	m.hint = text
	m.hintGen++
	gen := m.hintGen
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return hintExpireMsg{gen} })
}

func (m *Model) keyOver(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		if m.snap.IsHost && m.enoughToContinue() {
			m.room.Submit(room.NextHandCmd{ID: m.id})
		}
	}
	return m, nil
}

// enoughToContinue reports whether enough players are still connected (humans or bots)
// to deal another hand; below that the host can only quit.
func (m *Model) enoughToContinue() bool {
	if m.snap == nil {
		return false
	}
	alive := 0
	for _, p := range m.snap.Players {
		if p.Connected {
			alive++
		}
	}
	return alive >= m.snap.MinStart
}

func (m *Model) selectedCards() []game.Card {
	hand := m.hand()
	out := make([]game.Card, 0, len(m.selected))
	for i := 0; i < len(hand); i++ {
		if m.selected[i] {
			out = append(out, hand[i])
		}
	}
	return out
}

// computePlayable rebuilds which hand cards can still complete a legal play given the
// current selection, indexed by display position. It does not move the cursor. Off turn
// there is no set (every card reads low). Cheap enough to run on every state change.
func (m *Model) computePlayable() {
	if m.snap == nil || !m.isMyTurn() {
		m.playable = nil
		return
	}
	m.playable = game.PlayableSet(m.hand(), m.selectedCards(), m.snap.Table, m.snap.Opening, m.snap.OpenCard)
}

// recomputePlayable rebuilds the playable set and places the cursor. When your turn has
// just begun (a false->true edge), the cursor resets to the lowest playable card;
// otherwise it only snaps off a now-greyed card, keeping your place as the selection
// narrows. Use this whenever the selection or turn changes; use computePlayable alone
// when only the display order changed (a re-sort keeps the cursor on its own card).
func (m *Model) recomputePlayable() {
	myTurn := m.isMyTurn()
	turnBegan := myTurn && !m.wasMyTurn
	m.wasMyTurn = myTurn
	m.computePlayable()
	switch {
	case turnBegan:
		m.cursor = m.firstPlayable()
	case !m.cardPlayable(m.cursor):
		m.cursor = m.nearestPlayable(m.cursor)
	}
}

// firstPlayable returns the lowest (leftmost) playable card index, or 0 when nothing is
// playable (you can only pass).
func (m *Model) firstPlayable() int {
	for i := range m.playable {
		if m.playable[i] {
			return i
		}
	}
	return 0
}

// cardPlayable reports whether hand card i is currently playable (on your turn).
func (m *Model) cardPlayable(i int) bool {
	return i >= 0 && i < len(m.playable) && m.playable[i]
}

// stepCursor returns the next playable card index from the cursor in direction dir
// (+1 right, -1 left), skipping greyed cards; the cursor stays put if none is found.
func (m *Model) stepCursor(dir int) int {
	for i := m.cursor + dir; i >= 0 && i < len(m.playable); i += dir {
		if m.playable[i] {
			return i
		}
	}
	return m.cursor
}

// nearestPlayable returns the playable index closest to from (searching outward), or
// from unchanged when nothing is playable (you can only pass).
func (m *Model) nearestPlayable(from int) int {
	if m.cardPlayable(from) {
		return from
	}
	for d := 1; d < len(m.playable); d++ {
		if m.cardPlayable(from - d) {
			return from - d
		}
		if m.cardPlayable(from + d) {
			return from + d
		}
	}
	return from
}

// hand returns the viewer's hand in the current display order: by rank (the server
// order) or, when toggled, grouped by suit. Cursor and selection index into this.
func (m *Model) hand() []game.Card {
	if m.snap == nil {
		return nil
	}
	return sortHand(m.snap.YourHand, m.sortBySuit)
}

// sortHand returns a copy of hand ordered by rank (bySuit false) or by suit then
// rank (bySuit true).
func sortHand(hand []game.Card, bySuit bool) []game.Card {
	out := append([]game.Card(nil), hand...)
	sort.Slice(out, func(i, j int) bool {
		if bySuit {
			if out[i].Suit != out[j].Suit {
				return out[i].Suit < out[j].Suit
			}
			return out[i].Rank < out[j].Rank
		}
		return out[i].Order() < out[j].Order()
	})
	return out
}

// toggleSort flips the hand's sort order, keeping the same cards selected and the
// cursor on the same card as they move to their new positions.
func (m *Model) toggleSort() {
	old := m.hand()
	var cursorCard game.Card
	hasCursor := m.cursor >= 0 && m.cursor < len(old)
	if hasCursor {
		cursorCard = old[m.cursor]
	}
	selected := map[game.Card]bool{}
	for i, c := range old {
		if m.selected[i] {
			selected[c] = true
		}
	}
	m.sortBySuit = !m.sortBySuit
	m.selected = map[int]bool{}
	for i, c := range m.hand() {
		if selected[c] {
			m.selected[i] = true
		}
		if hasCursor && c == cursorCard {
			m.cursor = i
		}
	}
	m.computePlayable() // display indices changed; the cursor stays on its own card
}

// View renders the current screen, applying the boss-key disguise last.
func (m *Model) View() string {
	out := m.viewContent()
	if m.boss {
		out = bossHide(out)
	}
	return out
}

func (m *Model) viewContent() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.kicked != "" {
		return m.renderKicked() // shown at any size, so a kicked player always sees why
	}
	if m.w < minW || m.h < minH {
		return m.tooSmall()
	}
	if m.snap == nil {
		return m.center(m.st.secondary.Render("connecting..."))
	}
	switch m.snap.Phase {
	case protocol.Waiting:
		return m.renderWaiting()
	case protocol.InGame:
		return m.renderGame()
	case protocol.Finished:
		if m.winSlideActive() {
			return m.renderGame() // hold the board while the winning play slides in
		}
		return m.renderOver()
	}
	return ""
}

// winSlideActive reports that the hand just ended and the winning play is still
// sliding/holding in the pile, so the board should stay up before the scoreboard.
func (m *Model) winSlideActive() bool {
	return m.snap != nil && m.snap.Phase == protocol.Finished &&
		len(m.pileCur) > 0 && m.pileFinish == finishScore
}

func (m *Model) center(s string) string {
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, s)
}
