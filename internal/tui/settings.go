package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

// ruleField describes one radio row on the settings page: how to read and write its
// value in a Rules, and the label for each option. Left/right cycle through options.
type ruleField struct {
	name    string
	options []string
	get     func(game.Rules) int
	set     func(*game.Rules, int)
}

// ruleFields is the ordered list of configurable house rules shown on the settings page.
var ruleFields = []ruleField{
	{
		name:    "straights",
		options: []string{"big 2", "poker", "hong kong"},
		get:     func(r game.Rules) int { return int(r.Straights) },
		set:     func(r *game.Rules, v int) { r.Straights = game.StraightStyle(v) },
	},
	{
		name:    "flushes",
		options: []string{"high card", "suit then card"},
		get:     func(r game.Rules) int { return int(r.Flush) },
		set:     func(r *game.Rules, v int) { r.Flush = game.FlushRank(v) },
	},
	{
		name:    "passing",
		options: []string{"lockout", "re-enter"},
		get:     func(r game.Rules) int { return int(r.Pass) },
		set:     func(r *game.Rules, v int) { r.Pass = game.PassRule(v) },
	},
	{
		name:    "first play",
		options: []string{"low card", "winner leads"},
		get:     func(r game.Rules) int { return int(r.Lead) },
		set:     func(r *game.Rules, v int) { r.Lead = game.LeadRule(v) },
	},
}

// openSettings shows the host's house-rules page from the top of the list.
func (m *Model) openSettings() {
	m.settingsOpen = true
	m.settingsRow = 0
	m.editing = false
	m.editBuf = ""
}

// settingsRows is the total number of navigable rows: the rule radios plus one per
// reaction label.
func (m *Model) settingsRows() int {
	return len(ruleFields) + len(m.snap.Reactions)
}

// keySettings drives the settings page. It captures every key while the page is open,
// including esc (which closes it or cancels an edit).
func (m *Model) keySettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The page belongs to the host in the waiting room; anything else closes it.
	if m.snap == nil || m.snap.Phase != protocol.Waiting || !m.snap.IsHost {
		m.settingsOpen, m.editing = false, false
		return m, nil
	}
	if m.editing {
		return m.keyReactionEdit(k)
	}
	if m.settingsRow >= m.settingsRows() { // reactions count is fixed, but stay safe
		m.settingsRow = m.settingsRows() - 1
	}
	switch k.String() {
	case "esc", "o":
		m.settingsOpen = false
	case "up":
		if m.settingsRow > 0 {
			m.settingsRow--
		}
	case "down":
		if m.settingsRow < m.settingsRows()-1 {
			m.settingsRow++
		}
	case "left":
		m.cycleRule(-1)
	case "right":
		m.cycleRule(1)
	case "enter":
		if idx := m.settingsRow - len(ruleFields); idx >= 0 {
			m.editing = true
			m.editBuf = m.snap.Reactions[idx] // pre-fill so the host can tweak
		}
	}
	return m, nil
}

// cycleRule moves the option on the current rule row by dir (wrapping) and submits the
// updated ruleset. A no-op on a reaction row.
func (m *Model) cycleRule(dir int) {
	if m.settingsRow >= len(ruleFields) {
		return
	}
	f := ruleFields[m.settingsRow]
	next := (f.get(m.snap.Rules) + dir + len(f.options)) % len(f.options)
	r := m.snap.Rules
	f.set(&r, next)
	m.room.Submit(room.SetRulesCmd{ID: m.id, Rules: r})
}

// keyReactionEdit handles the inline reaction-label editor: printable keys append (up to
// MaxReactionLen runes), backspace deletes, enter saves, esc cancels.
func (m *Model) keyReactionEdit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.editing = false
	case tea.KeyEnter:
		if text := strings.TrimSpace(m.editBuf); text != "" {
			idx := m.settingsRow - len(ruleFields)
			m.room.Submit(room.SetReactionCmd{ID: m.id, Index: idx, Text: text})
		}
		m.editing = false
	case tea.KeyBackspace:
		if r := []rune(m.editBuf); len(r) > 0 {
			m.editBuf = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		s := string(k.Runes)
		if k.Type == tea.KeySpace {
			s = " "
		}
		if utf8.RuneCountInString(m.editBuf)+utf8.RuneCountInString(s) <= protocol.MaxReactionLen {
			m.editBuf += s
		}
	}
	return m, nil
}

// renderSettings draws the host's house-rules page: each rule on its current value, then
// the reaction labels, then a context legend.
func (m *Model) renderSettings() string {
	var b strings.Builder
	b.WriteString(m.st.primary.Render("settings") + "\n\n")
	for i, f := range ruleFields {
		b.WriteString(m.settingsRuleLine(i, f) + "\n")
	}
	b.WriteString("\n")
	for idx, label := range m.snap.Reactions {
		b.WriteString(m.settingsReactionLine(idx, label) + "\n")
	}
	b.WriteString("\n" + m.st.secondary.Render(m.settingsLegend()))
	return m.centerBlock(b.String())
}

// settingsRuleLine renders one rule row: name and its current value. The active row is
// marked and wraps its value in < > to signal that left/right change it.
func (m *Model) settingsRuleLine(row int, f ruleField) string {
	val := f.options[f.get(m.snap.Rules)]
	cursor := "  "
	if m.settingsRow == row {
		cursor = m.st.primary.Render("> ")
		val = "< " + val + " >"
	}
	return cursor + m.st.secondary.Render(fmt.Sprintf("%-11s", f.name)) + m.st.primary.Render(val)
}

// settingsReactionLine renders one reaction row: its bound key and label. The active row
// is marked; while editing, the label shows the buffer with a caret.
func (m *Model) settingsReactionLine(idx int, label string) string {
	cursor := "  "
	shown := label
	if m.settingsRow == len(ruleFields)+idx {
		cursor = m.st.primary.Render("> ")
		if m.editing {
			shown = m.editBuf + "_"
		}
	}
	return cursor + m.st.secondary.Render(fmt.Sprintf("%-3s", emoteKey(idx))) + m.st.primary.Render(shown)
}

// settingsLegend is the context-sensitive help at the foot of the settings page.
func (m *Model) settingsLegend() string {
	if m.editing {
		return strings.Join([]string{
			fmt.Sprintf("type a label (<=%d)", protocol.MaxReactionLen),
			"enter  save",
			"esc    cancel",
		}, "\n")
	}
	lines := []string{"up/down     move"}
	if m.settingsRow < len(ruleFields) {
		lines = append(lines, "left/right  change")
	} else {
		lines = append(lines, "enter       rename")
	}
	lines = append(lines, "esc         back")
	return strings.Join(lines, "\n")
}
