package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
	"github.com/Avangelista/big2-tui/internal/room"
)

// The settings page has two tab-switched sub-pages so each fits the minimum terminal:
// the rule radios, and the reaction labels.
const (
	pageRules = iota
	pageReactions

	reactCols = 2 // reaction labels are laid out in a fixed 2-column grid
	labelCol  = 6 // fixed label field width, so columns never shift as labels change
)

// ruleField describes one radio row on the rules page: how to read and write its value
// in a Rules, the label for each option, and a one-line explainer per option. Left/right
// cycle through the options.
type ruleField struct {
	name     string
	options  []string
	explains []string
	get      func(game.Rules) int
	set      func(*game.Rules, int)
}

// ruleFields is the ordered list of configurable house rules shown on the rules page.
var ruleFields = []ruleField{
	{
		name:    "straights",
		options: []string{"big 2", "poker", "hong kong"},
		explains: []string{
			"top out at 2, JQKA2 high",
			"A2345 low to 10JQKA high",
			"poker set, 2-wraps rank top",
		},
		get: func(r game.Rules) int { return int(r.Straights) },
		set: func(r *game.Rules, v int) { r.Straights = game.StraightStyle(v) },
	},
	{
		name:    "flushes",
		options: []string{"high card", "suit then card"},
		explains: []string{
			"top card wins, suit ties",
			"suit first, then top card",
		},
		get: func(r game.Rules) int { return int(r.Flush) },
		set: func(r *game.Rules, v int) { r.Flush = game.FlushRank(v) },
	},
	{
		name:    "passing",
		options: []string{"lockout", "re-enter"},
		explains: []string{
			"pass = out for the trick",
			"a fresh play reopens it",
		},
		get: func(r game.Rules) int { return int(r.Pass) },
		set: func(r *game.Rules, v int) { r.Pass = game.PassRule(v) },
	},
	{
		name:    "first play",
		options: []string{"low card", "winner leads"},
		explains: []string{
			"3D opens, must be played",
			"last winner opens freely",
		},
		get: func(r game.Rules) int { return int(r.Lead) },
		set: func(r *game.Rules, v int) { r.Lead = game.LeadRule(v) },
	},
}

// openSettings shows the host's house-rules page from the top of the rules tab.
func (m *Model) openSettings() {
	m.settingsOpen = true
	m.settingsPage = pageRules
	m.settingsRow = 0
	m.editing = false
	m.editBuf = ""
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
	switch k.String() {
	case "esc", "`", "~":
		m.settingsOpen = false
		return m, nil
	case "tab":
		m.switchPage()
		return m, nil
	}
	if m.settingsPage == pageRules {
		return m.keyRules(k)
	}
	return m.keyReactions(k)
}

// switchPage flips between the rules and reactions tabs, resetting the cursor.
func (m *Model) switchPage() {
	if m.settingsPage == pageRules {
		m.settingsPage = pageReactions
	} else {
		m.settingsPage = pageRules
	}
	m.settingsRow = 0
	m.editing = false
}

// keyRules navigates the rule radios (up/down) and cycles the current row (left/right).
func (m *Model) keyRules(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.settingsRow > 0 {
			m.settingsRow--
		}
	case "down":
		if m.settingsRow < len(ruleFields)-1 {
			m.settingsRow++
		}
	case "left":
		m.cycleRule(-1)
	case "right":
		m.cycleRule(1)
	}
	return m, nil
}

// keyReactions navigates the 2-column reaction grid (arrows) and starts an edit (enter).
func (m *Model) keyReactions(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.snap.Reactions)
	rows := reactRows(n)
	switch k.String() {
	case "up":
		if m.settingsRow%rows > 0 {
			m.settingsRow--
		}
	case "down":
		if m.settingsRow%rows < rows-1 && m.settingsRow+1 < n {
			m.settingsRow++
		}
	case "left":
		if m.settingsRow >= rows {
			m.settingsRow -= rows
		}
	case "right":
		if m.settingsRow+rows < n {
			m.settingsRow += rows
		}
	case "enter":
		if m.settingsRow < n {
			m.editing = true
			m.editBuf = m.snap.Reactions[m.settingsRow] // pre-fill so the host can tweak
		}
	}
	return m, nil
}

// reactRows is the number of grid rows for n reaction labels across reactCols columns.
func reactRows(n int) int { return (n + reactCols - 1) / reactCols }

// cycleRule moves the option on the current rule row by dir (wrapping) and submits the
// updated ruleset.
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
			m.room.Submit(room.SetReactionCmd{ID: m.id, Index: m.settingsRow, Text: text})
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

// renderSettings draws the active settings tab (rules or reactions) with a tab header
// and a context legend.
func (m *Model) renderSettings() string {
	// Render the active tab into a fixed-size box, then centre the box. The box's size
	// depends only on the static rule/reaction shapes (see settingsBox), never on which
	// tab is active, whether a label is being edited, or which option is selected - so
	// none of those actions re-centre the page and shift it.
	w, h := m.settingsBox()
	body := m.settingsBody(m.settingsPage, m.editing)
	return m.center(m.r.NewStyle().Width(w).Height(h).Render(body))
}

// settingsBody renders a settings tab in full: the tab header, the page content, and the
// context legend.
func (m *Model) settingsBody(page int, editing bool) string {
	var b strings.Builder
	b.WriteString(m.settingsTabs(page) + "\n\n")
	if page == pageRules {
		b.WriteString(m.renderRulesPage())
	} else {
		b.WriteString(m.renderReactionsPage())
	}
	b.WriteString("\n" + m.st.secondary.Render(m.settingsLegend(page, editing)))
	return b.String()
}

// settingsBox is the fixed content-box size (in cells) that holds either tab. Height is
// the taller tab; width covers the widest line any interaction can produce - every rule
// option and explainer, every legend variant, the tab header, and the reaction grid -
// so cycling a value or editing a label can never widen the box.
func (m *Model) settingsBox() (int, int) {
	h := max(lipgloss.Height(m.settingsBody(pageRules, false)), lipgloss.Height(m.settingsBody(pageReactions, false)))

	w := lipgloss.Width(m.settingsBody(pageReactions, false)) // fixed-width cells: constant
	w = max(w, lipgloss.Width(m.settingsTabs(pageRules)))
	for _, lg := range []string{
		m.settingsLegend(pageRules, false),
		m.settingsLegend(pageReactions, false),
		m.settingsLegend(pageReactions, true),
	} {
		for _, ln := range strings.Split(lg, "\n") {
			w = max(w, lipgloss.Width(ln))
		}
	}
	for _, f := range ruleFields {
		for i := range f.options {
			w = max(w, lipgloss.Width(m.ruleNameValue(false, f.name, f.options[i])))
			w = max(w, lipgloss.Width(m.ruleExplain(f.explains[i])))
		}
	}
	return w, h
}

// settingsTabs is the header showing both tabs with `page` lit.
func (m *Model) settingsTabs(page int) string {
	rules, react := "rules", "reactions"
	if page == pageRules {
		return m.st.primary.Render(rules) + "   " + m.st.tertiary.Render(react)
	}
	return m.st.tertiary.Render(rules) + "   " + m.st.primary.Render(react)
}

// renderRulesPage lists each rule on its current value with a one-line explainer. The
// explainer sits in a fixed row and only its text changes, so nothing shifts as the
// cursor or the selection moves.
func (m *Model) renderRulesPage() string {
	var b strings.Builder
	for i, f := range ruleFields {
		sel := f.get(m.snap.Rules)
		b.WriteString(m.ruleNameValue(m.settingsRow == i, f.name, f.options[sel]) + "\n")
		b.WriteString(m.ruleExplain(f.explains[sel]) + "\n")
	}
	return b.String()
}

// ruleNameValue renders a rule row's name and current value; ruleExplain the indented
// explainer line. Shared with the box-width measurement so it covers every option.
func (m *Model) ruleNameValue(active bool, name, value string) string {
	return m.settingsCursor(active) + m.st.secondary.Render(fmt.Sprintf("%-11s", name)) + m.st.primary.Render(value)
}

func (m *Model) ruleExplain(text string) string {
	return "    " + m.st.secondary.Render(text)
}

// renderReactionsPage lays the labels out in a fixed 2-column grid (column-major), each
// cell a fixed width so the grid never shifts as labels change or while editing.
func (m *Model) renderReactionsPage() string {
	n := len(m.snap.Reactions)
	rows := reactRows(n)
	var b strings.Builder
	for r := 0; r < rows; r++ {
		var cells []string
		for c := 0; c < reactCols; c++ {
			if idx := c*rows + r; idx < n {
				cells = append(cells, m.reactionCell(idx))
			}
		}
		b.WriteString(strings.Join(cells, "  ") + "\n")
	}
	return b.String()
}

// reactionCell renders one grid cell: cursor, bound key, and the label in a fixed field.
func (m *Model) reactionCell(idx int) string {
	label := m.snap.Reactions[idx]
	if m.settingsRow == idx && m.editing {
		label = m.editBuf + "_"
	}
	label = padDisp(label, labelCol)
	key := m.st.secondary.Render(fmt.Sprintf("%s ", emoteKey(idx)))
	return m.settingsCursor(m.settingsRow == idx) + key + m.st.primary.Render(label)
}

// settingsCursor is the 2-column active-row marker (blank when inactive), fixed width so
// it never shifts the row. It uses the same dot as the in-game hand cursor.
func (m *Model) settingsCursor(active bool) string {
	if active {
		return m.st.primary.Render("∙ ")
	}
	return "  "
}

// settingsLegend is the context-sensitive help at the foot of a settings tab.
func (m *Model) settingsLegend(page int, editing bool) string {
	if editing {
		return strings.Join([]string{
			fmt.Sprintf("type a label (<=%d)", protocol.MaxReactionLen),
			"enter save  esc cancel",
		}, "\n")
	}
	if page == pageRules {
		return strings.Join([]string{
			"up/down move  left/right change",
			"tab reactions  esc back",
		}, "\n")
	}
	return strings.Join([]string{
		"arrows move  enter rename",
		"tab rules  esc back",
	}, "\n")
}

// padDisp pads or clips s to a fixed display width w (rune/width aware, for unicode
// labels), so fixed-width fields stay aligned.
func padDisp(s string, w int) string {
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
