// Package bot is a dependency-free heuristic Big 2 player. It reasons only from
// public information (its own hand, the table, and opponents' counts).
package bot

import (
	"github.com/Avangelista/big2-tui/internal/game"
)

// Move is a bot's decision. Pass (no cards) is only legal while following.
type Move struct {
	Pass  bool
	Cards []game.Card
}

// ChooseMove picks a legal move for seat, always at full strength: follow the
// heuristic — shed low cards, win tricks cheaply, hold onto 2s and bombs for
// control, and dump once the hand is nearly empty.
func ChooseMove(g *game.GameState, seat game.Seat) Move {
	plays := g.LegalPlays(seat)
	if len(plays) == 0 {
		return Move{Pass: true}
	}
	endgame := len(g.Hands[seat]) <= 5

	if g.Table != nil { // following
		best := cheapest(plays)
		if !endgame && isPremium(best) && !leaderThreatening(g) {
			return Move{Pass: true} // save the power card; this trick isn't worth it
		}
		return Move{Cards: best.Cards}
	}

	// Leading.
	if endgame {
		return Move{Cards: mostCards(plays).Cards} // empty out as fast as possible
	}
	return Move{Cards: cheapest(plays).Cards} // shed the lowest single (never a lone 2 here)
}

// weaker orders by fewest cards, then by strength.
func weaker(a, b game.Combo) bool {
	if len(a.Cards) != len(b.Cards) {
		return len(a.Cards) < len(b.Cards)
	}
	return b.Beats(a)
}

func cheapest(plays []game.Combo) game.Combo {
	best := plays[0]
	for _, p := range plays[1:] {
		if weaker(p, best) {
			best = p
		}
	}
	return best
}

func mostCards(plays []game.Combo) game.Combo {
	best := plays[0]
	for _, p := range plays[1:] {
		if len(p.Cards) > len(best.Cards) || (len(p.Cards) == len(best.Cards) && weaker(p, best)) {
			best = p
		}
	}
	return best
}

func isLone2(c game.Combo) bool {
	return c.Type == game.Single && c.Key.Rank == game.Rank2
}

func isPremium(c game.Combo) bool {
	return isLone2(c) || c.Type == game.FourKind || c.Type == game.StraightFlush
}

// leaderThreatening reports whether the seat that owns the table is about to run
// out, so we shouldn't sit on our power cards.
func leaderThreatening(g *game.GameState) bool {
	return len(g.Hands[g.Leader]) <= 3
}
