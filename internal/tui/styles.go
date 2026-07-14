package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// styles is a two-step gray text hierarchy over the default foreground, plus the two
// card accents (red faces, blue ░ back). primary is the default fg (max contrast on
// any theme); secondary/tertiary recede to gray for peripheral text and markers.
// Card selection is geometry (the lift), not colour.
type styles struct {
	primary    lipgloss.Style // default fg: labels on turn, faces, prompts, headlines
	secondary  lipgloss.Style // gray: footer, inactive labels, markers, cursor
	tertiary   lipgloss.Style // dim gray: passed/gone/scroll, empty seats
	suitRed    lipgloss.Style // red card faces on your turn: hearts, diamonds
	suitRedDim lipgloss.Style // red faces when your hand is inactive (off turn)
	back       lipgloss.Style // opponent card-back ░ on their turn
}

func newStyles(r *lipgloss.Renderer) styles {
	gray := r.NewStyle().Foreground(lipgloss.Color("8")) // bright-black, tracks theme
	return styles{
		primary:    r.NewStyle(),
		secondary:  gray,
		tertiary:   gray.Faint(true), // degrades to secondary where Faint is ignored
		suitRed:    r.NewStyle().Foreground(lipgloss.Color("1")),
		suitRedDim: r.NewStyle().Foreground(lipgloss.Color("124")), // muted dark red
		back:       r.NewStyle().Foreground(lipgloss.Color("4")),   // blue
	}
}
