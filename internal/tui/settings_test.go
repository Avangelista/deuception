package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// waitingSnap is a waiting-room snapshot; host controls whether the viewer is the host.
func waitingSnap(host bool) protocol.StateSnapshot {
	return protocol.StateSnapshot{
		Phase: protocol.Waiting, Rev: 1, YouSeat: 0, IsHost: host,
		MaxSeats: 4, MinStart: 3,
		Players: []protocol.PlayerView{
			{Seat: 0, Letter: 'A', IsYou: true, IsHost: host, Connected: true},
		},
		Reactions: protocol.DefaultReactions(),
		Turn:      -1, TableBy: -1, Winner: -1,
	}
}

func openSettingsModel(t *testing.T, cc commander) *Model {
	t.Helper()
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: waitingSnap(true)})
	m.Update(runeKey('o'))
	if !m.settingsOpen {
		t.Fatal("host 'o' should open the settings page")
	}
	return m
}

func TestSettingsOpenAndRender(t *testing.T) {
	m := openSettingsModel(t, nopCommander{})
	frame := stripStyling(m.View())
	for _, want := range []string{"settings", "straights", "flushes", "passing", "first play", protocol.Emotes[0]} {
		if !strings.Contains(frame, want) {
			t.Errorf("settings frame missing %q", want)
		}
	}
}

func TestSettingsNonHostCannotOpen(t *testing.T) {
	cc := &captureCommander{}
	m := New(cc, "id", "hint", lipgloss.DefaultRenderer())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.Update(protocol.StateSnapshotMsg{Snap: waitingSnap(false)})
	m.Update(runeKey('o'))
	if m.settingsOpen {
		t.Fatal("a non-host must not open the settings page")
	}
	// For a non-host, 'o' is just a letter pick.
	if lc, ok := cc.last().(room.SetLetterCmd); !ok || lc.Letter != 'o' {
		t.Fatalf("non-host 'o' should submit SetLetterCmd 'o', got %#v", cc.last())
	}
}

func TestSettingsCycleRule(t *testing.T) {
	cc := &captureCommander{}
	m := openSettingsModel(t, cc)

	// Row 0 (straights): right cycles big 2 -> poker.
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if rc, ok := cc.last().(room.SetRulesCmd); !ok || rc.Rules.Straights != game.StraightsPoker {
		t.Fatalf("right on straights should submit poker, got %#v", cc.last())
	}

	// Down to passing (row 2), right cycles lockout -> re-enter, leaving other fields default.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	rc, ok := cc.last().(room.SetRulesCmd)
	if !ok || rc.Rules.Pass != game.PassReenter {
		t.Fatalf("right on passing should submit re-enter, got %#v", cc.last())
	}
	if rc.Rules.Straights != game.StraightsBig2 {
		t.Errorf("changing passing must not disturb straights: got %+v", rc.Rules)
	}
}

func TestSettingsSelectedValueReflectsSnapshot(t *testing.T) {
	m := openSettingsModel(t, nopCommander{})
	// Simulate the room echoing back a poker ruleset.
	s := waitingSnap(true)
	s.Rev = 2
	s.Rules = game.Rules{Straights: game.StraightsPoker}
	m.Update(protocol.StateSnapshotMsg{Snap: s})
	frame := stripStyling(m.View())
	if !strings.Contains(frame, "poker") {
		t.Errorf("settings should show the active straight style 'poker', got:\n%s", frame)
	}
}

func TestSettingsEditReaction(t *testing.T) {
	cc := &captureCommander{}
	m := openSettingsModel(t, cc)

	// Move to the first reaction row (rows 0-3 are rules).
	for i := 0; i < len(ruleFields); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // begin editing
	if !m.editing {
		t.Fatal("enter on a reaction row should start editing")
	}
	// The buffer pre-fills with the current label; clear it, then type a new one.
	for range protocol.Emotes[0] {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.Update(runeKey('y'))
	m.Update(runeKey('o'))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // save
	rc, ok := cc.last().(room.SetReactionCmd)
	if !ok || rc.Index != 0 || rc.Text != "yo" {
		t.Fatalf("saving should submit SetReactionCmd{0,\"yo\"}, got %#v", cc.last())
	}
	if m.editing {
		t.Error("saving should leave edit mode")
	}
}

func TestSettingsEditCapAndCancel(t *testing.T) {
	cc := &captureCommander{}
	m := openSettingsModel(t, cc)
	for i := 0; i < len(ruleFields); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for range protocol.Emotes[0] {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	// Typing is capped at MaxReactionLen runes.
	for _, r := range "abcdefgh" {
		m.Update(runeKey(r))
	}
	if len([]rune(m.editBuf)) != protocol.MaxReactionLen {
		t.Fatalf("edit buffer = %q, want capped to %d runes", m.editBuf, protocol.MaxReactionLen)
	}
	// esc cancels without submitting.
	before := len(cc.cmds)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing {
		t.Error("esc should leave edit mode")
	}
	if len(cc.cmds) != before {
		t.Error("esc should not submit a reaction change")
	}
}

func TestSettingsEscClosesWithoutQuit(t *testing.T) {
	cc := &captureCommander{}
	m := openSettingsModel(t, cc)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.settingsOpen {
		t.Fatal("esc should close the settings page")
	}
	for _, c := range cc.cmds {
		if _, ok := c.(room.QuitCmd); ok {
			t.Fatal("esc in settings must not quit the session")
		}
	}
	// Back on the waiting room (letter legend), not the settings page (rule rows).
	frame := stripStyling(m.View())
	if strings.Contains(frame, "straights") {
		t.Error("after esc the settings page should be gone")
	}
	if !strings.Contains(frame, "pick letter") {
		t.Error("after esc the waiting room should show")
	}
}
