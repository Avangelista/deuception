// Command preview renders TUI screens headlessly at fixed sizes so layout can be
// eyeballed without an SSH session. A dev aid.
package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
	"github.com/Avangelista/big2-tui/internal/tui"
)

type noopCommander struct{}

func (noopCommander) Submit(room.Command) {}

func hand(s string) []game.Card {
	cs, err := game.ParseCards(s)
	if err != nil {
		panic(err)
	}
	game.SortCards(cs)
	return cs
}

func render(title string, w, h int, snap protocol.StateSnapshot, cursor int, errText string) {
	m := tui.New(noopCommander{}, "rory", "ssh -p 2222 192.168.1.20", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(protocol.StateSnapshotMsg{Snap: snap})
	for range cursor {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if errText != "" {
		m.Update(protocol.ErrorMsg{Text: errText})
	}
	out := m.View()
	fmt.Printf("\n===== %s (%dx%d) =====\n", title, w, h)
	fmt.Println(strings.Repeat("-", w))
	fmt.Println(out)
	fmt.Println(strings.Repeat("-", w))
}

// renderPlay drives the model with key events to preview selection and cursor.
// selected lists card indices to toggle; cursor is where it ends up.
func renderPlay(title string, w, h int, snap protocol.StateSnapshot, selected []int, cursor int) {
	m := tui.New(noopCommander{}, "rory", "ssh -p 2222 192.168.1.20", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(protocol.StateSnapshotMsg{Snap: snap})
	cur := 0
	move := func(to int) {
		for cur < to {
			m.Update(tea.KeyMsg{Type: tea.KeyRight})
			cur++
		}
		for cur > to {
			m.Update(tea.KeyMsg{Type: tea.KeyLeft})
			cur--
		}
	}
	for _, idx := range selected {
		move(idx)
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	}
	move(cursor)
	fmt.Printf("\n===== %s (%dx%d) =====\n", title, w, h)
	fmt.Println(strings.Repeat("-", w))
	fmt.Println(m.View())
	fmt.Println(strings.Repeat("-", w))
}

// renderPile feeds a sequence of plays (each a new table combo) and renders the
// board with the play-in slide settled, so the pile shows its resting centred card.
func renderPile(title string, w, h int, base protocol.StateSnapshot, plays []string, by []int) {
	m := tui.New(noopCommander{}, "rory", "ssh -p 2222 192.168.1.20", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	for i, p := range plays {
		s := base
		s.Table = hand(p)
		s.TableBy = by[i]
		s.Rev = i + 1
		m.Update(protocol.StateSnapshotMsg{Snap: s})
	}
	m.SettlePile() // headless: no tick loop, so land the slide at rest
	fmt.Printf("\n===== %s (%dx%d) =====\n", title, w, h)
	fmt.Println(strings.Repeat("-", w))
	fmt.Println(m.View())
	fmt.Println(strings.Repeat("-", w))
}

// renderBoss renders a snapshot with the hide-card-UI toggle on (borders blanked).
func renderBoss(title string, w, h int, snap protocol.StateSnapshot) {
	m := tui.New(noopCommander{}, "rory", "ssh -p 2222 192.168.1.20", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(protocol.StateSnapshotMsg{Snap: snap})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}) // toggle "hide card UI" on
	fmt.Printf("\n===== %s (%dx%d) =====\n", title, w, h)
	fmt.Println(strings.Repeat("-", w))
	fmt.Println(m.View())
	fmt.Println(strings.Repeat("-", w))
}

func main() {
	// 4-player mid game
	fourP := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 3, IsYou: true, IsTurn: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 5, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
			{Seat: 3, CardCount: 2, Connected: true},
		},
		YourHand: hand("4D 4H 2S"),
		Table:    hand("6D 6H 6S"), Turn: 0, TableBy: -1, Winner: -1,
	}
	render("4-player game", 80, 24, fourP, 2, "")

	topActive := fourP
	topActive.Turn = 2
	topActive.Players = append([]protocol.PlayerView(nil), fourP.Players...)
	topActive.Players[0].IsTurn = false
	topActive.Players[2].IsTurn = true
	render("4-player, TOP (C) on turn", 80, 24, topActive, 2, "")

	fivePile := fourP
	fivePile.Table = hand("3D 4D 5D 6D 7D")
	render("4-player, 5-card pile", 80, 24, fivePile, 2, "")

	big := fourP
	big.YourHand = hand("3D 3C 4D 4H 5S 7C 7H 9D TS JC QH KD 2S")
	big.Players = append([]protocol.PlayerView(nil), fourP.Players...)
	big.Players[0].CardCount = 13
	render("4-player, 13-card hand", 140, 40, big, 3, "")
	render("4-player, narrow", 54, 20, big, 8, "")
	render("4-player, scrolled (hand windows)", 40, 18, big, 6, "")
	render("4-player, min size", 34, 18, big, 6, "")

	threeP := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 7, IsYou: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 5, IsTurn: true, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
		},
		YourHand: hand("4D 4H 5C 8D TS JH 2S"),
		Table:    nil, Turn: 1, TableBy: -1, Winner: -1,
	}
	render("3-player game", 80, 24, threeP, 0, "")

	waiting := protocol.StateSnapshot{
		Phase: protocol.Waiting, YouSeat: 0, IsHost: true, MaxSeats: 4, MinStart: 2,
		Players: []protocol.PlayerView{
			{Seat: 0, IsYou: true, IsHost: true, Connected: true},
			{Seat: 1, Connected: true},
			{Seat: 2, Connected: true},
		},
		Turn: -1, Winner: -1,
	}
	render("waiting room (host, 3 joined)", 80, 24, waiting, 0, "")
	render("waiting room (34 wide)", 34, 18, waiting, 0, "")

	// Waiting room with chosen letters and a bot seated.
	withBots := protocol.StateSnapshot{
		Phase: protocol.Waiting, YouSeat: 0, IsHost: true, MaxSeats: 4, MinStart: 2,
		Players: []protocol.PlayerView{
			{Seat: 0, Letter: 'R', IsYou: true, IsHost: true, Connected: true},
			{Seat: 1, Letter: 'K', Connected: true},
			{Seat: 2, Letter: 'Q', IsBot: true, BotLevel: 7, Connected: true},
		},
		Turn: -1, Winner: -1,
	}
	render("waiting room (chosen letters + a bot)", 80, 24, withBots, 0, "")
	render("waiting room (non-host view)", 80, 24, func() protocol.StateSnapshot {
		s := withBots
		s.YouSeat, s.IsHost = 1, false
		s.Players = append([]protocol.PlayerView(nil), withBots.Players...)
		s.Players[0].IsYou, s.Players[1].IsYou = false, true
		return s
	}(), 0, "")

	over := protocol.StateSnapshot{
		Phase: protocol.Finished, YouSeat: 0, IsHost: true, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 0, IsYou: true, IsHost: true, Connected: true, Score: 0},
			{Seat: 1, CardCount: 7, Connected: true, Score: 14},
			{Seat: 2, CardCount: 3, Connected: true, Score: 3},
			{Seat: 3, CardCount: 13, Connected: true, Score: 52, IsBot: true, BotLevel: 4},
		},
		Turn: -1, Winner: 0,
	}
	render("game over + scoreboard", 80, 24, over, 0, "")

	// 2-player game: only top and bottom seats.
	twoP := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 2,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 17, IsYou: true, IsTurn: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 17, Connected: true},
		},
		YourHand: hand("3D 4D 5S 6C 7H 8D 9S TC JD QH KS AD 2S 3C 4H 5D 6S"),
		Table:    nil, Turn: 0, TableBy: -1, Winner: -1,
	}
	render("2-player game (top + bottom only)", 80, 24, twoP, 0, "")

	// Error line above the hand.
	render("error above the hand", 80, 24, fourP, 2, "does not beat the current play")
	render("error at min size", 34, 18, fourP, 2, "does not beat the current play")

	// Self-hand selection: raised = selected, * = cursor.
	sel := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 2,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 7, IsYou: true, IsTurn: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 7, Connected: true},
		},
		YourHand: hand("3D 4H 5C 8D TS JH 2S"),
		Table:    nil, Turn: 0, TableBy: -1, Winner: -1,
	}
	// cards 1,3,5,6 selected (raised); cursor on a low card (0).
	renderPlay("self-hand: selected raised, cursor on LOW card", 80, 24, sel, []int{1, 3, 5, 6}, 0)
	// cursor on a high (selected) card (1) - * should ride up with it.
	renderPlay("self-hand: cursor on HIGH card", 80, 24, sel, []int{1, 3, 5, 6}, 1)
	// none selected, cursor mid-hand.
	renderPlay("self-hand: none selected, cursor on 3", 80, 24, sel, nil, 3)

	// Off-turn: no cursor, but "<"/">" and arrow scroll still work. render() sends
	// N right-arrows (= scroll off turn).
	offScroll := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 2,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 13, IsYou: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 13, IsTurn: true, Connected: true},
		},
		YourHand: hand("3D 3C 4D 4H 5S 7C 7H 9D TS JC QH KD 2S"),
		Table:    nil, TableBy: -1, Turn: 1, Winner: -1,
	}
	render("off-turn hand, not scrolled (> only)", 40, 20, offScroll, 0, "")
	render("off-turn hand, scrolled right x3 (< and >)", 40, 20, offScroll, 3, "")

	// Last-play arrow + pass X's. C (top) played the table; D (right) and A (self,
	// bottom) passed; B (left) is on turn. Arrow points from C down to the pile.
	arrows := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 3, IsYou: true, Connected: true, IsHost: true, Passed: true},
			{Seat: 1, CardCount: 5, IsTurn: true, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
			{Seat: 3, CardCount: 2, Connected: true, Passed: true},
		},
		YourHand: hand("4D 4H 2S"),
		Table:    hand("6D 6H 6S"), Turn: 1, TableBy: 2, Winner: -1,
	}
	render("arrows: C played (v), D+A passed (X), B on turn", 80, 24, arrows, 0, "")

	// B (left) played the table (arrow >); C (top) + D (right) passed (X); A on turn.
	sideArrow := arrows
	sideArrow.Players = []protocol.PlayerView{
		{Seat: 0, CardCount: 3, IsYou: true, IsTurn: true, Connected: true, IsHost: true},
		{Seat: 1, CardCount: 5, Connected: true},
		{Seat: 2, CardCount: 6, Connected: true, Passed: true},
		{Seat: 3, CardCount: 2, Connected: true, Passed: true},
	}
	sideArrow.Turn, sideArrow.TableBy = 0, 1
	render("arrows: B played (>), C+D passed (X), A on turn", 80, 24, sideArrow, 0, "")

	// A (self, bottom) played the table (arrow ^ above own hand); B on turn.
	selfArrow := arrows
	selfArrow.Players = []protocol.PlayerView{
		{Seat: 0, CardCount: 3, IsYou: true, Connected: true, IsHost: true},
		{Seat: 1, CardCount: 5, IsTurn: true, Connected: true},
		{Seat: 2, CardCount: 6, Connected: true},
		{Seat: 3, CardCount: 2, Connected: true},
	}
	selfArrow.Turn, selfArrow.TableBy = 1, 0
	render("arrows: A (self) played (^ above hand), B on turn", 80, 24, selfArrow, 0, "")

	// 1-card opponents: each side fan must draw exactly one card (no phantom).
	oneCard := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 5, IsYou: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 1, IsTurn: true, Connected: true},
			{Seat: 2, CardCount: 1, Connected: true},
			{Seat: 3, CardCount: 1, Connected: true},
		},
		YourHand: hand("4D 4H 5C 8D 2S"),
		Table:    nil, Turn: 1, TableBy: -1, Winner: -1,
	}
	render("1-card opponents (B on turn) - one card each side", 80, 24, oneCard, 0, "")

	// Dead player: D disconnected -> "d" marker, auto-passes.
	dead := protocol.StateSnapshot{
		Phase: protocol.InGame, YouSeat: 0, MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, CardCount: 3, IsYou: true, IsTurn: true, Connected: true, IsHost: true},
			{Seat: 1, CardCount: 5, Connected: true},
			{Seat: 2, CardCount: 6, Connected: true},
			{Seat: 3, CardCount: 8, Connected: false}, // left the game
		},
		YourHand: hand("4D 4H 2S"),
		Table:    nil, Turn: 0, TableBy: -1, Winner: -1,
	}
	render("dead player: D left (d marker), A on turn", 80, 24, dead, 0, "")

	// Pile at rest: the newest play sits centred, covering the ones it beat. The
	// slide-in (from the player's side) is a runtime animation, not shown here.
	pileBase := fourP
	pileBase.Table = nil
	pileBase.TableBy = -1
	pairs := []string{"3D 3C", "6H 6S", "TD TH", "KD KH"}
	pairBy := []int{2, 3, 1, 0} // top, right, left, self
	renderPile("pile at rest: newest pair, centred (4 plays)", 80, 24, pileBase, pairs, pairBy)
	renderPile("pile at rest, min size (34x14)", 34, 14, pileBase, pairs, pairBy)
	fives := []string{"3D 4D 5D 6D 7D", "9D TD JD QD KD"}
	fiveBy := []int{2, 3} // top, right
	renderPile("pile at rest: wide 5-card play, centred", 80, 24, pileBase, fives, fiveBy)
	renderPile("pile at rest: 5-card play, min size (34x14)", 34, 14, pileBase, fives, fiveBy)

	// Boss key: same board with all | and _ blanked out.
	renderBoss("hide card UI ON: borders blanked (looks like plain text)", 80, 24, fourP)

	render("too small", 30, 10, fourP, 0, "")
}
