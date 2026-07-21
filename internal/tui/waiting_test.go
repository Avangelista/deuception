package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

// TestWaitingBotKeys: bots are added with . or > and removed with , or < (the shifted
// keys work too, but the labels say </> and the unshifted keys don't need shift).
func TestWaitingBotKeys(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: waitingSnap(true)})

	for _, k := range []rune{'.', '>'} {
		cc.cmds = nil
		m.Update(runeKey(k))
		if _, ok := cc.last().(room.AddBotCmd); !ok {
			t.Errorf("%q should add a bot, got %#v", string(k), cc.last())
		}
	}
	for _, k := range []rune{',', '<'} {
		cc.cmds = nil
		m.Update(runeKey(k))
		if _, ok := cc.last().(room.RemoveBotCmd); !ok {
			t.Errorf("%q should remove a bot, got %#v", string(k), cc.last())
		}
	}
	// The legend advertises the labelled keys.
	if !strings.Contains(stripStyling(m.View()), "</>    remove/add bot") {
		t.Error("waiting legend should show '</>    remove/add bot'")
	}
}

// TestWaitingReactionKeys: the reaction keys fire in the waiting room, just like the
// score screen. The keys that used to add/remove bots (+ = -) are now reactions.
func TestWaitingReactionKeys(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: waitingSnap(true)})

	m.Update(runeKey('3')) // preset 2
	if ec, ok := cc.last().(room.EmoteCmd); !ok || ec.Code != 2 {
		t.Fatalf("digit 3 in the waiting room should send EmoteCmd code 2, got %#v", cc.last())
	}
	cc.cmds = nil
	m.Update(runeKey('-')) // now a reaction, not remove-bot
	if ec, ok := cc.last().(room.EmoteCmd); !ok || ec.Code != 10 {
		t.Fatalf("'-' should now send EmoteCmd code 10, got %#v", cc.last())
	}
}

// TestWaitingReactionShows: a reaction flashes beside its player's roster row and does
// not shift the roster (the column is always reserved).
func TestWaitingReactionShows(t *testing.T) {
	players := []protocol.PlayerView{
		{Seat: 0, Letter: 'R', IsYou: true, IsHost: true, Connected: true},
		{Seat: 1, Letter: 'K', Connected: true},
		{Seat: 2, Letter: 'Q', IsBot: true, Connected: true},
	}
	s := protocol.StateSnapshot{
		Phase: protocol.Waiting, Rev: 1, YouSeat: 0, IsHost: true, MaxSeats: 4, MinStart: 2,
		Players: players, Turn: -1, Winner: -1,
	}
	m := New(nopCommander{}, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: s})

	idle := contentTopLeft(stripStyling(m.View()))
	m.emotes = map[int]emoteState{1: {3, 1}} // K reacts

	frame := stripStyling(m.View())
	if !strings.Contains(frame, protocol.Emotes[3]) {
		t.Errorf("reaction %q should show in the waiting roster", protocol.Emotes[3])
	}
	if got := contentTopLeft(frame); got != idle {
		t.Errorf("a reaction shifted the waiting room: idle %v, reacting %v", idle, got)
	}
}
