package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// styles keeps the board plain. The only colours are the card face (red for
// hearts/diamonds, default for spades/clubs - rank and suit alike) and an
// opponent's card-back, which turns blue and fills with a ░ pattern on their turn.
// The turn cue for the viewer stays the [brackets]; cursor/selection are geometry
// (the "*" and the lift), not colour.
type styles struct {
	faint   lipgloss.Style
	turn    lipgloss.Style
	suitRed lipgloss.Style // red card faces: hearts, diamonds
	back    lipgloss.Style // opponent card-back on their turn
}

func newStyles(r *lipgloss.Renderer) styles {
	plain := r.NewStyle()
	return styles{
		faint:   plain,
		turn:    plain, // turn emphasis stays the [brackets], not colour
		suitRed: r.NewStyle().Foreground(lipgloss.Color("1")),
		back:    r.NewStyle().Foreground(lipgloss.Color("4")), // blue
	}
}
