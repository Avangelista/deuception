package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Avangelista/deuception/internal/game"
	"github.com/Avangelista/deuception/internal/protocol"
	"github.com/Avangelista/deuception/internal/room"
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
		Table:    parseHand(t, "6H 6S"), TableBy: 1, Turn: 0, Winner: -1,
	}
	m.Update(protocol.StateSnapshotMsg{Snap: s})

	if !m.midPlaySlide() || m.isMyTurn() {
		t.Fatal("turn should not activate while the card is still sliding in")
	}
	if strings.Contains(m.selfBand(), "[") {
		t.Fatal("your hand should not show the on-turn bracket mid-slide")
	}

	for m.pileStep < pileSteps {
		m.Update(pileAnimMsg{gen: m.pileGen})
	}
	if m.midPlaySlide() || !m.isMyTurn() {
		t.Fatal("turn should activate once the card lands")
	}
	if !strings.Contains(m.selfBand(), "[") {
		t.Fatal("your hand should show the on-turn bracket after the card lands")
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
