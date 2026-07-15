package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

// TestBossHidePlainAndAligned: the boss-key frame carries no escapes and every row
// keeps the same display width as the live (coloured, glyph) frame, so the disguise
// is column-identical plain text.
func TestBossHidePlainAndAligned(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	m := New(nopCommander{}, "id", "hint", r)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: fourPTableSnap(1,
		parseHand(t, "4D 4H 5C 8D TS JH 2S"), parseHand(t, "6H 6S"), 1)})
	m.pileStep = pileSteps
	live := m.View()
	m.boss = true
	boss := m.View()
	if strings.ContainsRune(boss, 0x1b) {
		t.Error("boss frame should carry no colour escapes")
	}
	lv, bv := strings.Split(live, "\n"), strings.Split(boss, "\n")
	if len(lv) != len(bv) {
		t.Fatalf("line count differs: live %d boss %d", len(lv), len(bv))
	}
	for i := range lv {
		if lipgloss.Width(lv[i]) != lipgloss.Width(bv[i]) {
			t.Errorf("row %d width differs: live %d boss %d", i, lipgloss.Width(lv[i]), lipgloss.Width(bv[i]))
		}
	}
}

type nopCommander struct{}

func (nopCommander) Submit(room.Command) {}

// glyphCol returns the leftmost display column of glyph in a rendered frame, or -1.
func glyphCol(frame string, glyph rune) int {
	best := -1
	for _, line := range strings.Split(frame, "\n") {
		s := stripStyling(line)
		if i := strings.IndexRune(s, glyph); i >= 0 {
			if col := len([]rune(s[:i])); best == -1 || col < best {
				best = col
			}
		}
	}
	return best
}

// topAlignSnap builds an np-player in-game snapshot with the top opponent either on
// turn (topPassed=false) or passed with you on turn (topPassed=true). No table, so no
// pile slide interferes with the turn cue.
func topAlignSnap(np int, hand []game.Card, topSeat int, topPassed bool) protocol.StateSnapshot {
	players := make([]protocol.PlayerView, np)
	for i := range players {
		players[i] = protocol.PlayerView{Seat: i, CardCount: 5, Connected: true}
	}
	players[0].IsYou = true
	players[0].CardCount = len(hand)
	turn := topSeat
	if topPassed {
		players[topSeat].Passed = true
		turn = 0
	}
	players[turn].IsTurn = true
	return protocol.StateSnapshot{
		Phase: protocol.InGame, Rev: 1, YouSeat: 0,
		Players: players, YourHand: hand, Turn: turn, TableBy: -1, Winner: -1,
	}
}

// colOf returns the display column (width, so pips and variation selectors count
// correctly) of the first row containing sub, or -1.
func colOf(block, sub string) int {
	for _, ln := range strings.Split(block, "\n") {
		s := stripStyling(ln)
		if i := strings.Index(s, sub); i >= 0 {
			return lipgloss.Width(s[:i])
		}
	}
	return -1
}

// TestLabelStacksCountOverInitial: the card-count indicator stacks the count directly
// above the player's initial in one column, in every seat and turn state. A top
// opponent idling off turn (no ✗) once shifted the initial left by the empty mark slot;
// on the self hand the count must clear the "›" more-cards flag with the initial beneath.
func TestLabelStacksCountOverInitial(t *testing.T) {
	// Top opponent (seat 2, letter B, 8 cards) in each turn state.
	top := func(state int) *Model {
		players := []protocol.PlayerView{
			{Seat: 0, Letter: 'R', IsYou: true, IsTurn: state != 2, CardCount: 7, Connected: true},
			{Seat: 1, Letter: 'A', CardCount: 5, Connected: true},
			{Seat: 2, Letter: 'B', CardCount: 8, Connected: true},
			{Seat: 3, Letter: 'C', CardCount: 4, Connected: true},
		}
		turn := 0
		switch state {
		case 1:
			players[2].Passed = true
		case 2:
			players[2].IsTurn, turn = true, 2
		}
		m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
		m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
		m.Update(protocol.StateSnapshotMsg{Snap: protocol.StateSnapshot{
			Phase: protocol.InGame, Rev: 1, YouSeat: 0, Players: players,
			YourHand: parseHand(t, "3D 4C 5C 6C 7S 8D 9H"), Turn: turn, TableBy: -1, Winner: -1}})
		return m
	}
	for _, tc := range []struct {
		name  string
		state int
	}{{"idle off turn", 0}, {"passed", 1}, {"on turn", 2}} {
		t.Run("top "+tc.name, func(t *testing.T) {
			band := top(tc.state).topBand(4, 60)
			cCol, lCol := colOf(band, "8"), colOf(band, "B")
			if cCol < 0 || lCol < 0 {
				t.Fatalf("missing count(%d)/initial(%d) in\n%s", cCol, lCol, band)
			}
			if cCol != lCol {
				t.Errorf("count col %d != initial col %d\n%s", cCol, lCol, band)
			}
		})
	}

	// Self hand, narrow so the "›" more-cards flag shows: the count sits right of the
	// flag and the initial lines up beneath the count.
	t.Run("self with more-cards flag", func(t *testing.T) {
		m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
		m.Update(tea.WindowSizeMsg{Width: 42, Height: 22})
		m.Update(protocol.StateSnapshotMsg{Snap: protocol.StateSnapshot{
			Phase: protocol.InGame, Rev: 1, YouSeat: 0,
			Players:  []protocol.PlayerView{{Seat: 0, Letter: 'R', IsYou: true, IsTurn: true, CardCount: 12, Connected: true}},
			YourHand: parseHand(t, "3D 4C 5C 6C 7S 8D 9H TC JD JS KH 2S"), Turn: 0, TableBy: -1, Winner: -1}})
		m.cursor = 0
		m.recomputePlayable() // left end: the "›" flag shows on the right
		band := m.selfBand()
		flag, cCol, lCol := colOf(band, "›"), colOf(band, "12"), colOf(band, "R")
		if flag < 0 || cCol < 0 || lCol < 0 {
			t.Fatalf("missing flag(%d)/count(%d)/initial(%d) in\n%s", flag, cCol, lCol, band)
		}
		if cCol <= flag {
			t.Errorf("count col %d should be right of the › flag at %d\n%s", cCol, flag, band)
		}
		if cCol != lCol {
			t.Errorf("count col %d != initial col %d\n%s", cCol, lCol, band)
		}
	})
}

// TestReactDigitSends: a number key sends the matching quick-chat reaction, picker open
// or not, on or off turn; a digit past the preset range does nothing.
func TestReactDigitSends(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 9H"))})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // "hurry" (code 2)
	if ec, ok := cc.last().(room.EmoteCmd); !ok || ec.Code != 2 {
		t.Fatalf("digit 3 should send EmoteCmd code 2, got %#v", cc.last())
	}
	cc.cmds = nil
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}}) // no 9th preset
	if cc.last() != nil {
		t.Fatalf("a digit past the presets should not send, got %#v", cc.last())
	}
}

// TestReactPickerToggle: r opens/closes the picker, which swaps the footer for the preset
// legend; the digits work regardless.
func TestReactPickerToggle(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 9H"))})
	if strings.Contains(m.gameFooter(80), "lol") {
		t.Fatal("closed footer should show controls, not presets")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if !m.reacting || !strings.Contains(m.gameFooter(80), "lol") {
		t.Fatal("r should open the picker and list presets")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.reacting {
		t.Fatal("r again should close the picker")
	}
}

// TestPickerStaysOpenOnReact: sending a reaction with the picker open leaves it open so
// you can fire several; r closes it.
func TestPickerStaysOpenOnReact(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 9H"))})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // open
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // react (why, code 1)
	if !m.reacting {
		t.Fatal("the picker should stay open after sending a reaction")
	}
	if ec, ok := cc.last().(room.EmoteCmd); !ok || ec.Code != 1 {
		t.Fatalf("the digit should still send while the picker is open, got %#v", cc.last())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}) // close
	if m.reacting {
		t.Fatal("r should close the picker")
	}
}

// TestScrollNoLayoutShift: scrolling the hand never shifts the cards - the ‹/› more-cards
// flags occupy reserved slots so the band width (and its centring) stays constant.
func TestScrollNoLayoutShift(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 20})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1,
		parseHand(t, "3D 3C 4C 5C 6C 7S 8D 9H TC JD JS KH 2S"))})
	fanCol := func() int {
		for _, ln := range strings.Split(m.renderGame(), "\n") {
			s := stripStyling(ln)
			if strings.ContainsAny(s, "♦♣♥♠") {
				return lipgloss.Width(s[:strings.IndexRune(s, '│')])
			}
		}
		return -1
	}
	var cols []int
	for _, cur := range []int{0, 3, 6, 9, 12} { // left end .. right end
		m.cursor = cur
		m.recomputePlayable()
		cols = append(cols, fanCol())
	}
	for i := 1; i < len(cols); i++ {
		if cols[i] != cols[0] {
			t.Errorf("hand shifted while scrolling: first │ at col %d (step %d), want %d", cols[i], i, cols[0])
		}
	}
}

// TestQuitConfirmFlow: esc in-game asks first (no quit); esc cancels, enter confirms.
func TestQuitConfirmFlow(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 9H"))})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.confirmQuit {
		t.Fatal("esc in-game should raise the quit confirm")
	}
	if _, ok := cc.last().(room.QuitCmd); ok {
		t.Fatal("the first esc should not quit")
	}
	if !strings.Contains(m.gameFooter(80), "quit?") {
		t.Fatal("the footer should ask to confirm")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel
	if m.confirmQuit {
		t.Fatal("a second esc should cancel the confirm")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})            // raise again
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	if _, ok := cc.last().(room.QuitCmd); !ok {
		t.Fatalf("enter should quit, got %#v", cc.last())
	}
	if cmd == nil {
		t.Fatal("confirming should return a quit command")
	}
}

// TestReactionsOnInitialLine: a reaction flashes on a seat's initial line (not the count
// line), inward of the initial for the side opponents.
func TestReactionsOnInitialLine(t *testing.T) {
	players := []protocol.PlayerView{
		{Seat: 0, Letter: 'R', IsYou: true, IsTurn: true, CardCount: 9, Connected: true},
		{Seat: 1, Letter: 'A', CardCount: 5, Connected: true},
		{Seat: 2, Letter: 'B', CardCount: 8, Connected: true},
		{Seat: 3, Letter: 'C', CardCount: 4, Connected: true},
	}
	snap := protocol.StateSnapshot{Phase: protocol.InGame, Rev: 1, YouSeat: 0,
		Players: players, YourHand: parseHand(t, "3D 4C 5C 6C 7S 8D 9H TC 2S"),
		Turn: 0, TableBy: -1, Winner: -1}
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 72, Height: 22})
	m.Update(protocol.StateSnapshotMsg{Snap: snap})
	m.emotes = map[int]emoteState{0: {0, 1}, 1: {1, 1}, 2: {2, 1}, 3: {3, 1}} // lol why hurry bro

	frame := stripStyling(m.renderGame())
	for _, e := range []string{"lol", "why", "hurry", "bro"} {
		if !strings.Contains(frame, e) {
			t.Errorf("reaction %q missing from frame", e)
		}
	}
	// Left opponent: reaction on the initial row (last), not the count row above it.
	lrows := strings.Split(m.sideBlock(players[1], 8, true), "\n")
	initRow, countRow := stripStyling(lrows[len(lrows)-1]), stripStyling(lrows[len(lrows)-2])
	if !strings.Contains(initRow, "A") || !strings.Contains(initRow, "why") {
		t.Errorf("left reaction should share the initial row, got %q", initRow)
	}
	if strings.Contains(countRow, "why") {
		t.Errorf("reaction should not be on the count row, got %q", countRow)
	}
	// Right opponent: reaction sits inward (left) of the initial.
	rrows := strings.Split(m.sideBlock(players[3], 8, false), "\n")
	rrow := stripStyling(rrows[len(rrows)-1])
	if strings.Index(rrow, "bro") > strings.Index(rrow, "C") {
		t.Errorf("right reaction should sit left of the initial, got %q", rrow)
	}
}

// TestTopPointerAlignsWithMarker: the top opponent's on-turn ▴ pointer and its
// off-turn ✗ marker must land in the same column at any width (they use different
// centring primitives, so even widths used to be off by one).
func TestTopPointerAlignsWithMarker(t *testing.T) {
	hand := parseHand(t, "4D 4H 5C 8D TS JH 2S")
	render := func(w int, snap protocol.StateSnapshot) string {
		m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		m.Update(protocol.StateSnapshotMsg{Snap: snap})
		return m.renderGame()
	}
	for _, w := range []int{80, 81, 64, 40, 34} {
		for _, np := range []int{4, 2} {
			topSeat := 2
			if np == 2 {
				topSeat = 1
			}
			ptr := glyphCol(render(w, topAlignSnap(np, hand, topSeat, false)), '▴')
			mark := glyphCol(render(w, topAlignSnap(np, hand, topSeat, true)), '✗')
			if ptr < 0 || mark < 0 {
				t.Fatalf("w=%d np=%d: missing cue (▴=%d ✗=%d)", w, np, ptr, mark)
			}
			if ptr != mark {
				t.Errorf("w=%d np=%d: top ▴ col %d != ✗ col %d", w, np, ptr, mark)
			}
		}
	}
}

// captureCommander records submitted commands for assertions.
type captureCommander struct{ cmds []room.Command }

func (c *captureCommander) Submit(cmd room.Command) { c.cmds = append(c.cmds, cmd) }

func (c *captureCommander) last() room.Command {
	if len(c.cmds) == 0 {
		return nil
	}
	return c.cmds[len(c.cmds)-1]
}

func parseHand(t *testing.T, s string) []game.Card {
	t.Helper()
	cs, err := game.ParseCards(s)
	if err != nil {
		t.Fatalf("ParseCards(%q): %v", s, err)
	}
	return cs
}

func inGameSnap(rev int, h []game.Card) protocol.StateSnapshot {
	return protocol.StateSnapshot{
		Phase:   protocol.InGame,
		Rev:     rev,
		YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, IsTurn: true, CardCount: len(h)},
		},
		YourHand: h,
		Turn:     0,
		TableBy:  -1,
		Winner:   -1,
	}
}

// TestSelectionResetsOnEqualSizeRedeal: an equal-size redeal must still clear
// stale selection indices.
func TestSelectionResetsOnEqualSizeRedeal(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	h1 := parseHand(t, "3D 4H 5C 8D TS JH 2S") // 7 cards
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, h1)})
	m.selected[1] = true
	m.selected[3] = true

	h2 := parseHand(t, "3C 4D 5S 8C TD JD 2C") // 7 different cards
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(2, h2)})
	if len(m.selected) != 0 {
		t.Fatalf("selection should reset on an equal-size redeal, got %v", m.selected)
	}
}

// TestSelectionPersistsWhenHandUnchanged: an unchanged hand (opponent's move)
// must keep our pending selection.
func TestSelectionPersistsWhenHandUnchanged(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	h := parseHand(t, "3D 4H 5C 8D TS JH 2S")
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, h)})
	m.selected[2] = true

	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(2, h)})
	if !m.selected[2] {
		t.Fatalf("selection should persist while the hand is unchanged")
	}
}

// tableSnap is an in-game snapshot with a two-player pile: seat 0 (you) leads, the
// given table combo was just played by tableBy.
func tableSnap(rev int, yourHand, table []game.Card, tableBy int) protocol.StateSnapshot {
	return protocol.StateSnapshot{
		Phase:   protocol.InGame,
		Rev:     rev,
		YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, CardCount: len(yourHand), Connected: true},
			{Seat: 1, CardCount: 5, Connected: true},
		},
		YourHand: yourHand,
		Table:    table,
		TableBy:  tableBy,
		Turn:     0,
		Winner:   -1,
	}
}

// cardCol returns the leftmost display column of a card's face in a rendered frame,
// or -1. Used to track where the pile card sits. (Glyph/colour-aware: see pileColOf.)
func cardCol(frame, face string) int { return pileColOf(frame, face) }

// TestPileSlidesFromSideToCentre: a play by the top opponent (seat 1, drawn above)
// starts at the top of the mid region and glides to centre; at rest it is centred
// and only the current card shows.
func TestPileSlidesFromSideToCentre(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	hand := parseHand(t, "4D 4H 5C 8D TS JH 2S")
	// A 2-player pile: seat 1 is the top opponent, so its play slides down (dy<0
	// origin = top of the block). Lead first so there is a play on the table.
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(1, hand, parseHand(t, "3D 3C"), 0)})
	m.SettlePile()
	if m.pileStep != pileSteps {
		t.Fatalf("lead should settle; step=%d want %d", m.pileStep, pileSteps)
	}

	// Now seat 1 (top) beats it. Direction must point up (dy negative).
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(2, hand, parseHand(t, "6H 6S"), 1)})
	if m.pileDir != [2]int{0, -1} {
		t.Fatalf("top play direction = %v, want {0,-1}", m.pileDir)
	}
	if m.pileStep != 0 {
		t.Fatalf("new play should start at step 0, got %d", m.pileStep)
	}
	if !sameHand(m.pilePrev, parseHand(t, "3D 3C")) {
		t.Fatalf("previous play not retained for the cover, got %v", m.pilePrev)
	}

	// The card starts fully above the block (off-screen, clipped), so it isn't drawn
	// at step 0. Track the "6H" face row once it appears: it must only move down
	// (a top play slides in from the top) and finish below where it first showed.
	rowOf := func() int { return pileRowOf(m.View(), "6H") }
	if rowOf() >= 0 {
		t.Fatal("incoming card should start off-screen (clipped), not fully drawn")
	}
	firstRow, lastRow := -1, -1
	for m.pileStep < pileSteps {
		m.Update(pileAnimMsg{gen: m.pileGen})
		if r := rowOf(); r >= 0 {
			if firstRow == -1 {
				firstRow = r
			}
			if lastRow != -1 && r < lastRow {
				t.Fatalf("card moved up (%d -> %d); a top play should slide down", lastRow, r)
			}
			lastRow = r
		}
	}
	if firstRow == -1 {
		t.Fatal("incoming card never slid into view")
	}
	if lastRow <= firstRow {
		t.Fatalf("card did not slide down: first visible row %d, end row %d", firstRow, lastRow)
	}
	if m.pilePrev != nil {
		t.Fatalf("covered play should be dropped once settled, got %v", m.pilePrev)
	}
	// At rest, exactly one pile card (the current 6H/6S) is present, not the old 3s.
	frame := m.View()
	if cardCol(frame, "3D") >= 0 || cardCol(frame, "3C") >= 0 {
		t.Fatal("beaten play still visible at rest; the cover should hide it")
	}
}

// TestStaleSnapshotIgnored: an out-of-order lower-rev snapshot is dropped, not
// applied over current state.
func TestStaleSnapshotIgnored(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	newer := parseHand(t, "3D 4H 5C 8D TS JH 2S")
	older := parseHand(t, "3C 4D 5S 8C TD JD 2C")
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(5, newer)})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(3, older)}) // arrives late
	if !sameHand(m.snap.YourHand, newer) {
		t.Fatalf("a stale (lower-rev) snapshot must be ignored, got %v", m.snap.YourHand)
	}
}

// TestTurnActivatesAfterSlide: the player the turn passes to only becomes active
// (hand lifts, bracket shows) once the played card has finished sliding in.
func TestTurnActivatesAfterSlide(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Seat 1 plays; the turn passes to you (seat 0), and their card slides in.
	s := protocol.StateSnapshot{
		Phase: protocol.InGame, Rev: 1, YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, IsTurn: true, CardCount: 3, Connected: true},
			{Seat: 1, CardCount: 5, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
			{Seat: 3, CardCount: 2, Connected: true},
		},
		YourHand: parseHand(t, "4D 4H 2S"),
		Table:    parseHand(t, "6H"), TableBy: 1, Turn: 0, Winner: -1,
	}
	m.Update(protocol.StateSnapshotMsg{Snap: s})

	if !m.midPlaySlide() || m.isMyTurn() {
		t.Fatal("turn should not activate while the card is still sliding in")
	}
	if strings.Contains(m.selfBand(), "∙") {
		t.Fatal("your hand should not show the on-turn cursor mid-slide")
	}

	for m.pileStep < pileSteps {
		m.Update(pileAnimMsg{gen: m.pileGen})
	}
	if m.midPlaySlide() || !m.isMyTurn() {
		t.Fatal("turn should activate once the card lands")
	}
	if !strings.Contains(m.selfBand(), "∙") {
		t.Fatal("your hand should show the on-turn cursor after the card lands")
	}
}

// tableTurnSnap builds a your-turn in-game snapshot with a combo already on the table
// (played by seat 1), so PlayableSet narrows the hand once the slide settles.
func tableTurnSnap(rev int, hand, table []game.Card) protocol.StateSnapshot {
	return protocol.StateSnapshot{
		Phase: protocol.InGame, Rev: rev, YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, IsTurn: true, CardCount: len(hand), Connected: true},
			{Seat: 1, CardCount: 5, Connected: true},
		},
		YourHand: hand, Table: table, TableBy: 1, Turn: 0, Winner: -1,
	}
}

// TestTurnResetsCursorToLowestPlayable: when your turn begins the cursor jumps to the
// lowest (leftmost) playable card, discarding any stale position from a prior turn.
func TestTurnResetsCursorToLowestPlayable(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.cursor = 6 // stale, from a previous turn
	// A single ten is down; only TS (by suit), JD, KD, 2S beat it. Lowest is TS (index 3).
	m.Update(protocol.StateSnapshotMsg{Snap: tableTurnSnap(1,
		parseHand(t, "3D 5C 9H TS JD KD 2S"), parseHand(t, "TC"))})
	m.SettlePile() // land the opponent's play so your turn activates
	if got := m.hand()[m.cursor]; got.String() != "TS" {
		t.Fatalf("cursor should reset to the lowest playable card TS, got %s (index %d)", got, m.cursor)
	}
}

// TestGreyedCardsUnselectableAndSkipped: a card that can't complete a legal play greys
// out - the cursor steps over it and space refuses to select it.
func TestGreyedCardsUnselectableAndSkipped(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Leading with 3S selected: the other 3s (pair/triple), the spade flush and the
	// 4-7 straight all stay live; 9C greys out. Hand sorts to [3D 3C 3S 4S 5S 6S 7S 9C].
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3S 3D 3C 4S 5S 6S 7S 9C"))})
	m.cursor = 2 // 3S
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !m.selected[2] {
		t.Fatal("space should have selected 3S")
	}
	nine := len(m.hand()) - 1 // 9C is the last (highest) card
	if m.cardPlayable(nine) {
		t.Fatal("9C should be greyed once 3S is selected")
	}
	// Stepping right past the straight never lands on the greyed 9C.
	for i := 0; i < len(m.hand()); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		if m.cursor == nine {
			t.Fatalf("cursor landed on the greyed 9C at index %d", nine)
		}
	}
	// Even forced onto 9C, space refuses to select it.
	m.cursor = nine
	before := len(m.selected)
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if len(m.selected) != before {
		t.Fatal("space should not select a greyed card")
	}
}

// TestEnterQuickPlaysCursorCard: with nothing selected, enter plays the single card
// under the cursor; with a selection, enter plays the selection.
func TestEnterQuickPlaysCursorCard(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 7H TD"))})

	// Nothing selected: enter quick-plays the card under the cursor.
	m.cursor = 2
	want := m.hand()[2]
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pc, ok := cc.last().(room.PlayCmd)
	if !ok {
		t.Fatalf("enter should submit a PlayCmd, got %T", cc.last())
	}
	if len(pc.Cards) != 1 || pc.Cards[0] != want {
		t.Fatalf("quick-play sent %v, want [%v]", pc.Cards, want)
	}

	// With a selection, enter plays the selection, not the cursor card.
	cc.cmds = nil
	m.selected[0], m.selected[1] = true, true
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pc2, _ := cc.last().(room.PlayCmd); len(pc2.Cards) != 2 {
		t.Fatalf("with a selection, enter should play the 2 selected cards, got %v", pc2.Cards)
	}
}

// TestHandSortToggle: `s` flips the hand between rank order and suit order, keeping
// the same cards selected and the cursor on the same card.
func TestHandSortToggle(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	raw := parseHand(t, "3D 5C 7H TD 5D 2S")
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, raw)})

	byRank, bySuit := sortHand(raw, false), sortHand(raw, true)
	if !sameHand(m.hand(), byRank) {
		t.Fatalf("default hand should be rank-sorted, got %v", m.hand())
	}
	if sameHand(byRank, bySuit) {
		t.Fatal("test hand needs rank and suit orders to differ")
	}

	// Select two cards and put the cursor on a third (display indices).
	m.selected[0], m.selected[2] = true, true
	m.cursor = 4
	wantSel := map[game.Card]bool{byRank[0]: true, byRank[2]: true}
	wantCursor := byRank[4]

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !m.sortBySuit || !sameHand(m.hand(), bySuit) {
		t.Fatalf("s should switch to suit order, got %v", m.hand())
	}
	dh := m.hand()
	gotSel := map[game.Card]bool{}
	for i := range dh {
		if m.selected[i] {
			gotSel[dh[i]] = true
		}
	}
	if len(gotSel) != len(wantSel) {
		t.Fatalf("selection size changed across sort: got %d want %d", len(gotSel), len(wantSel))
	}
	for c := range wantSel {
		if !gotSel[c] {
			t.Errorf("selected card %v lost across the sort toggle", c)
		}
	}
	if dh[m.cursor] != wantCursor {
		t.Errorf("cursor moved off its card: on %v, want %v", dh[m.cursor], wantCursor)
	}

	// Toggle back to rank order.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.sortBySuit || !sameHand(m.hand(), byRank) {
		t.Fatal("s should toggle back to rank order")
	}
}

// TestWindowIndices pins the cursor-centred hand-window math: centre, clamp at both
// ends, and the moreLeft/moreRight scroll flags.
func TestWindowIndices(t *testing.T) {
	tests := []struct {
		n, cursor, maxCells int
		start, end          int
		left, right         bool
	}{
		{13, 0, 5, 0, 5, false, true},   // at the left end
		{13, 12, 5, 8, 13, true, false}, // at the right end
		{13, 6, 5, 4, 9, true, true},    // centred, both flags
		{4, 2, 5, 0, 4, false, false},   // whole hand fits
	}
	for _, tc := range tests {
		start, end, left, right := windowIndices(tc.n, tc.cursor, tc.maxCells)
		if start != tc.start || end != tc.end || left != tc.left || right != tc.right {
			t.Errorf("windowIndices(%d,%d,%d) = (%d,%d,%v,%v), want (%d,%d,%v,%v)",
				tc.n, tc.cursor, tc.maxCells, start, end, left, right,
				tc.start, tc.end, tc.left, tc.right)
		}
	}
}

// fourPTableSnap is a 4-player in-game snapshot (you at seat 0) with table played
// by tableBy - used to drive the horizontal side-opponent slides.
func fourPTableSnap(rev int, yourHand, table []game.Card, tableBy int) protocol.StateSnapshot {
	return protocol.StateSnapshot{
		Phase:   protocol.InGame,
		Rev:     rev,
		YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, CardCount: len(yourHand), Connected: true},
			{Seat: 1, CardCount: 5, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
			{Seat: 3, CardCount: 5, Connected: true},
		},
		YourHand: yourHand,
		Table:    table,
		TableBy:  tableBy,
		Turn:     0,
		Winner:   -1,
	}
}

// TestPileSelfPlaySlidesUp: when you play (TableBy == YouSeat, dir {0,1}) the pile
// slides up from the bottom edge into centre.
func TestPileSelfPlaySlidesUp(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	hand := parseHand(t, "4D 4H 5C 8D TS JH 2S")
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(1, hand, parseHand(t, "6H 6S"), 0)})
	if m.pileDir != [2]int{0, 1} {
		t.Fatalf("self-play direction = %v, want {0,1}", m.pileDir)
	}
	rowOf := func() int { return pileRowOf(m.View(), "6H") }
	firstRow, lastRow := -1, -1
	for m.pileStep < pileSteps {
		m.Update(pileAnimMsg{gen: m.pileGen})
		if r := rowOf(); r >= 0 {
			if firstRow == -1 {
				firstRow = r
			}
			if lastRow != -1 && r > lastRow {
				t.Fatalf("card moved down (%d -> %d); a self play should slide up", lastRow, r)
			}
			lastRow = r
		}
	}
	if firstRow == -1 {
		t.Fatal("self-play card never slid into view")
	}
	if lastRow >= firstRow {
		t.Fatalf("card did not slide up: first visible row %d, end row %d", firstRow, lastRow)
	}
}

// TestPileHorizontalSlides: a side opponent's play slides in horizontally from its
// edge - the right seat slides left, the left seat slides right.
func TestPileHorizontalSlides(t *testing.T) {
	hand := parseHand(t, "4D 4H 5C 8D TS JH 2S")
	table := parseHand(t, "6H 6S")
	colOf := func(m *Model) int { return pileColOf(m.View(), "6H") }
	run := func(tableBy int, wantDir [2]int, leftward bool) {
		m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m.Update(protocol.StateSnapshotMsg{Snap: fourPTableSnap(1, hand, table, tableBy)})
		if m.pileDir != wantDir {
			t.Fatalf("tableBy %d: dir = %v, want %v", tableBy, m.pileDir, wantDir)
		}
		first, last := -1, -1
		for m.pileStep < pileSteps {
			m.Update(pileAnimMsg{gen: m.pileGen})
			if c := colOf(m); c >= 0 {
				if first == -1 {
					first = c
				}
				last = c
			}
		}
		if first == -1 {
			t.Fatalf("tableBy %d: card never slid into view", tableBy)
		}
		if leftward && last >= first {
			t.Fatalf("tableBy %d: expected leftward slide, first col %d end %d", tableBy, first, last)
		}
		if !leftward && last <= first {
			t.Fatalf("tableBy %d: expected rightward slide, first col %d end %d", tableBy, first, last)
		}
	}
	run(3, [2]int{1, 0}, true)   // right opponent -> slides left into centre
	run(1, [2]int{-1, 0}, false) // left opponent -> slides right into centre
}

// TestWinningPlayAnimatesThenScoreboard: a game-winning play arrives as a finished
// snapshot with the winning card still on the table. The board holds while the card
// slides in, then the view switches to the scoreboard once it settles.
func TestWinningPlayAnimatesThenScoreboard(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A play is on the table and settled; then you empty your hand and win.
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(1, parseHand(t, "2S"), parseHand(t, "3D"), 1)})
	m.SettlePile()

	win := protocol.StateSnapshot{
		Phase:   protocol.Finished,
		Rev:     2,
		YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, CardCount: 0, Connected: true},
			{Seat: 1, CardCount: 5, Connected: true, Score: 7},
		},
		Table:   parseHand(t, "2S"),
		TableBy: 0,
		Turn:    0,
		Winner:  0,
	}
	m.Update(protocol.StateSnapshotMsg{Snap: win})

	// While the winning card slides in, the board is shown (footer legend present),
	// not the scoreboard.
	if !m.winSlideActive() {
		t.Fatal("winning play should start a slide, not settle instantly")
	}
	during := m.View()
	if strings.Contains(during, "wins") {
		t.Fatal("scoreboard shown before the winning card finished sliding in")
	}
	if !strings.Contains(during, "esc quit") {
		t.Fatal("board (footer) should be shown while the winning play slides in")
	}

	// Drive the slide to completion; the card then holds at centre (still the board,
	// not the scoreboard).
	for i := 0; i < pileSteps+2 && m.pileStep < pileSteps; i++ {
		m.Update(pileAnimMsg{gen: m.pileGen})
	}
	if !m.winSlideActive() {
		t.Fatal("winning card should hold at centre before the scoreboard")
	}
	if strings.Contains(m.View(), "wins") {
		t.Fatal("scoreboard shown during the centre hold")
	}
	// End the hold: now the scoreboard takes over.
	m.Update(pileFinishMsg{gen: m.pileGen})
	if m.winSlideActive() {
		t.Fatal("win reveal should be finished after the hold ends")
	}
	if !strings.Contains(m.View(), "wins") {
		t.Fatal("scoreboard should show once the winning card has been revealed")
	}
}

// TestOnlyWinnerHandHidden: only the player who threw their last card (now at 0)
// renders an empty hand; every other player still shows their cards.
func TestOnlyWinnerHandHidden(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Opponent C (seat 2, top) plays their last card and wins; you (seat 0) and the
	// side opponents still hold cards.
	win := protocol.StateSnapshot{
		Phase: protocol.Finished, Rev: 1, YouSeat: 0,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, CardCount: 4, Connected: true},
			{Seat: 1, CardCount: 5, Connected: true},
			{Seat: 2, CardCount: 0, Connected: true},
			{Seat: 3, CardCount: 3, Connected: true},
		},
		YourHand: parseHand(t, "4D 5H 8C 2S"),
		Table:    parseHand(t, "2H"), TableBy: 2, Turn: 2, Winner: 2,
	}
	m.Update(protocol.StateSnapshotMsg{Snap: win})
	m.pileStep = pileSteps // land the slide

	if strings.Contains(m.topBand(4, 80), "│") {
		t.Error("the winner's hand (0 cards) should show no cards")
	}
	if !strings.Contains(m.sideBlock(m.playerAtRel(1), 8, true), "│") {
		t.Error("left opponent still holds cards and should show them")
	}
	if !strings.Contains(m.sideBlock(m.playerAtRel(3), 8, false), "│") {
		t.Error("right opponent still holds cards and should show them")
	}
	if !strings.Contains(m.selfBand(), "│") {
		t.Error("your own hand still holds cards and should show them")
	}
	if pileColOf(m.renderGame(), "2H") < 0 {
		t.Error("winning card should still land in the pile")
	}

	// And when you are the one who wins, your emptied hand shows no cards.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	selfWin := win
	selfWin.Rev = 2
	selfWin.YourHand = nil
	selfWin.Players = []protocol.PlayerView{
		{Seat: 0, IsYou: true, CardCount: 0, Connected: true},
		{Seat: 1, CardCount: 5, Connected: true},
		{Seat: 2, CardCount: 6, Connected: true},
		{Seat: 3, CardCount: 3, Connected: true},
	}
	selfWin.Table = parseHand(t, "2S")
	selfWin.TableBy, selfWin.Turn, selfWin.Winner = 0, 0, 0
	m.Update(protocol.StateSnapshotMsg{Snap: selfWin})
	if strings.Contains(m.selfBand(), "│") {
		t.Error("your emptied hand should show no cards")
	}
	if !strings.Contains(m.sideBlock(m.playerAtRel(1), 8, true), "│") {
		t.Error("opponents who still hold cards should show them when you win")
	}
}

// TestPileLingersWhenTrickResetsMidSlide: if the table clears while a play is still
// sliding in (a trick won the instant it was played, e.g. disconnected opponents
// auto-passing), the card finishes sliding and holds, then clears - it isn't cut off.
func TestPileLingersWhenTrickResetsMidSlide(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// A play lands and starts sliding in.
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(1, parseHand(t, "4D 5D"), parseHand(t, "6H 6S"), 1)})
	if m.pileStep >= pileSteps {
		t.Fatal("play should start a slide")
	}
	// The trick resets immediately (empty table) while still mid-slide.
	m.Update(protocol.StateSnapshotMsg{Snap: tableSnap(2, parseHand(t, "4D 5D"), nil, -1)})
	if len(m.pileCur) == 0 {
		t.Fatal("card was cleared mid-slide; it should linger to finish")
	}
	if m.pileFinish != finishClear {
		t.Fatalf("expected finishClear pending after a mid-slide reset, got %v", m.pileFinish)
	}

	// Drive the slide to completion; the card is still shown during the hold.
	for i := 0; i < pileSteps+2 && m.pileStep < pileSteps; i++ {
		m.Update(pileAnimMsg{gen: m.pileGen})
	}
	if len(m.pileCur) == 0 {
		t.Fatal("card should still be shown while it holds at centre")
	}
	// End the hold: the pile clears.
	m.Update(pileFinishMsg{gen: m.pileGen})
	if len(m.pileCur) != 0 {
		t.Fatalf("pile should clear after the card lands and holds, got %v", m.pileCur)
	}
}

// TestOffTurnScrollClamps: off turn, scrolling a hand wider than the screen keeps
// m.scroll within [0, len-maxHandCells], and widening re-clamps it back into range.
func TestOffTurnScrollClamps(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 24}) // narrow: not all cards fit

	hand := parseHand(t, "3D 3C 4D 4H 5S 7C 7H 9D TS JC QH KD 2S") // 13 cards
	snap := inGameSnap(1, hand)
	snap.Players = []protocol.PlayerView{
		{Seat: 0, IsYou: true, CardCount: len(hand)},
		{Seat: 1, IsTurn: true, CardCount: 5},
	}
	snap.Turn = 1 // seat 1 is on turn, so we are off turn
	m.Update(protocol.StateSnapshotMsg{Snap: snap})
	if m.isMyTurn() {
		t.Fatal("setup: viewer should be off turn")
	}

	for i := 0; i < 30; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		if maxS := len(hand) - m.maxHandCells(); m.scroll > maxS || m.scroll < 0 {
			t.Fatalf("scroll %d out of range [0,%d] after right #%d", m.scroll, maxS, i)
		}
	}
	if m.scroll == 0 {
		t.Fatal("off-turn right never scrolled; window may be too wide for the test")
	}

	// Widen enough that the whole hand fits: scroll must re-clamp to 0.
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 24})
	if m.scroll != 0 {
		t.Fatalf("scroll should re-clamp to 0 when the hand fits, got %d", m.scroll)
	}
}

// TestEndScreenDisconnected: the scoreboard tags players who left, and the host is only
// offered a next hand while enough players remain.
func TestEndScreenDisconnected(t *testing.T) {
	over := func(conn []bool, minStart int) string {
		players := make([]protocol.PlayerView, len(conn))
		for i := range players {
			players[i] = protocol.PlayerView{Seat: i, Letter: byte('A' + i), Connected: conn[i]}
		}
		players[0].IsYou, players[0].IsHost = true, true
		m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
		m.Update(tea.WindowSizeMsg{Width: 50, Height: 16})
		m.Update(protocol.StateSnapshotMsg{Snap: protocol.StateSnapshot{
			Phase: protocol.Finished, Rev: 1, YouSeat: 0, IsHost: true, MinStart: minStart,
			Players: players, Winner: 0, Turn: -1}})
		return stripStyling(m.renderOver())
	}
	// One left, three remain (>= minStart 3): tagged, host can deal.
	got := over([]bool{true, true, true, false}, 3)
	if !strings.Contains(got, "(disconnected)") {
		t.Error("a disconnected player should be tagged on the scoreboard")
	}
	if !strings.Contains(got, "next hand") {
		t.Error("host with enough players should be offered the next hand")
	}
	// Two left, two remain (< minStart 3): host can only quit.
	got = over([]bool{true, true, false, false}, 3)
	if strings.Contains(got, "next hand") {
		t.Error("host should not be offered a next hand when too few remain")
	}
	if !strings.Contains(got, "not enough players") {
		t.Error("host should be told there aren't enough players")
	}
}

// TestKickExemptFromMinSize: the kick screen shows even on a tiny terminal, while the
// game still asks to enlarge below the minimum.
func TestKickExemptFromMinSize(t *testing.T) {
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 20, Height: 8}) // below minW/minH
	m.Update(protocol.StateSnapshotMsg{Snap: inGameSnap(1, parseHand(t, "3D 5C 9H"))})
	if !strings.Contains(stripStyling(m.View()), "enlarge") {
		t.Error("below the minimum the game should still ask to enlarge")
	}
	m.kicked = "game already in progress"
	out := stripStyling(m.View())
	if strings.Contains(out, "enlarge") || !strings.Contains(out, "game already in progress") {
		t.Errorf("kick screen should show on a tiny window, got:\n%s", out)
	}
}
