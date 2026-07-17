package bot

import (
	"math/rand"
	"testing"

	"github.com/Avangelista/big2-tui/internal/game"
)

func mustCards(t *testing.T, s string) []game.Card {
	t.Helper()
	cs, err := game.ParseCards(s)
	if err != nil {
		t.Fatalf("ParseCards(%q): %v", s, err)
	}
	game.SortCards(cs)
	return cs
}

// followGame builds a following position: seat 0 to move against a table combo,
// with the leader (seat 1) holding leaderCards so leaderThreatening is testable.
func followGame(t *testing.T, hand0, table, leaderCards string) *game.GameState {
	t.Helper()
	g := game.NewGame(2, nil) // firstPlay defaults to false, so no open-card rule
	g.Hands[0] = mustCards(t, hand0)
	g.Hands[1] = mustCards(t, leaderCards)
	tbl, err := game.Classify(mustCards(t, table), game.SimpleStraight)
	if err != nil {
		t.Fatalf("classify table %q: %v", table, err)
	}
	g.Table = &tbl
	g.Turn, g.Leader = 0, 1
	g.Started = true
	return g
}

// TestChooseMovePassesWhenStuck: no legal play means the bot passes.
func TestChooseMovePassesWhenStuck(t *testing.T) {
	g := followGame(t, "3C 3S 8D", "4H 4S", "9C 9H TC")
	mv := ChooseMove(g, 0)
	if !mv.Pass {
		t.Fatalf("expected pass with no beating play, got %v", mv.Cards)
	}
}

// TestChooseMoveHoldsBombWhenSafe: a strong bot sits on its lone 2 rather than
// spend it to win an unimportant trick while the leader is far from finishing.
func TestChooseMoveHoldsBombWhenSafe(t *testing.T) {
	// Only 2S beats the 9 on the table; leader holds 5 cards (not threatening).
	g := followGame(t, "2S 3C 4D 5D 6D 7C 8C", "9H", "TC JC QC KC AC")
	mv := ChooseMove(g, 0)
	if !mv.Pass {
		t.Fatalf("strong bot should hold its 2, got %v", mv.Cards)
	}
}

// TestChooseMoveSpendsBombWhenLeaderThreatens: with the leader about to run out,
// the bot spends the same 2 to break the trick.
func TestChooseMoveSpendsBombWhenLeaderThreatens(t *testing.T) {
	g := followGame(t, "2S 3C 4D 5D 6D 7C 8C", "9H", "TC JC") // leader down to 2 cards
	mv := ChooseMove(g, 0)
	if mv.Pass {
		t.Fatal("bot should spend its 2 while the leader is threatening")
	}
	if got := game.CardsString(mv.Cards); got != "2S" {
		t.Fatalf("played %s, want 2S", got)
	}
}

// TestChooseMovePlaysFullGame is the property test: across many seeds the bot never
// produces an illegal move and every game terminates.
func TestChooseMovePlaysFullGame(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := game.NewGame(4, nil)
		if err := g.Deal(rng); err != nil {
			t.Fatalf("seed %d: deal: %v", seed, err)
		}
		for step := 0; step < 1000 && !g.Finished; step++ {
			seat := g.Turn
			mv := ChooseMove(g, seat)
			var err error
			if mv.Pass {
				_, err = g.Pass(seat)
			} else {
				_, err = g.Play(seat, mv.Cards)
			}
			if err != nil {
				t.Fatalf("seed %d step %d: illegal bot move (pass=%v cards=%v): %v",
					seed, step, mv.Pass, mv.Cards, err)
			}
		}
		if !g.Finished {
			t.Fatalf("seed %d: game never finished", seed)
		}
	}
}
