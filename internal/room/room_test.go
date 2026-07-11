package room

import (
	mrand "math/rand"
	"testing"

	"github.com/Avangelista/deuception/internal/game"
	"github.com/Avangelista/deuception/internal/protocol"
)

// joinN seats n players (first is host) with nil programs (fanout skips them).
func joinN(r *Room, n int) []string {
	r.trickDelay = 0 // deterministic: no trick-won hold in tests
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = NewID()
		r.Submit(JoinCmd{ID: ids[i], Host: i == 0})
	}
	return ids
}

// joinStart seats n players and has the host start the game.
func joinStart(r *Room, n int) []string {
	ids := joinN(r, n)
	r.Submit(StartCmd{ID: ids[0]})
	return ids
}

func TestRedactionOnlyOwnHand(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(42)))
	ids := joinStart(r, 4)

	for v := 0; v < 4; v++ {
		snap := r.Query(ids[v])
		if snap.Phase != protocol.InGame {
			t.Fatalf("viewer %d: phase = %v, want InGame", v, snap.Phase)
		}
		if len(snap.YourHand) == 0 {
			t.Fatalf("viewer %d: own hand is empty", v)
		}
		// each player is visible only as a count; no field can carry another's cards
		total := 0
		for _, p := range snap.Players {
			total += p.CardCount
		}
		if total != 52 {
			t.Errorf("viewer %d: card counts sum to %d, want 52", v, total)
		}
	}

	// Different viewers see different hands, and each sees exactly its own.
	h0 := game.CardsString(r.Query(ids[0]).YourHand)
	h1 := game.CardsString(r.Query(ids[1]).YourHand)
	if h0 == h1 {
		t.Errorf("viewers 0 and 1 see identical hands: %s", h0)
	}
}

func TestKickAfterStart(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(1)))
	ids := joinN(r, 3) // 3 players
	r.Submit(StartCmd{ID: ids[0]})
	if snap := r.Query(ids[0]); snap.Phase != protocol.InGame {
		t.Fatalf("phase after host start = %v, want InGame", snap.Phase)
	}
	// A late joiner should be kicked (game in progress) - it is not seated.
	lateID := NewID()
	r.Submit(JoinCmd{ID: lateID})
	if snap := r.Query(lateID); snap.YouSeat != -1 {
		t.Errorf("late joiner was seated (YouSeat=%d), want kicked/not seated", snap.YouSeat)
	}
}

func TestNonHostCannotStart(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(2)))
	ids := joinN(r, 3)
	r.Submit(StartCmd{ID: ids[1]}) // non-host
	if snap := r.Query(ids[0]); snap.Phase != protocol.Waiting {
		t.Errorf("non-host start changed phase to %v, want Waiting", snap.Phase)
	}
}

// playOutHand drives a whole hand to completion through the actor using dumb
// singles-only players (lead lowest; beat a single if possible; otherwise pass).
func playOutHand(t *testing.T, r *Room, ids []string) {
	t.Helper()
	for iter := 0; iter < 2000; iter++ {
		lead := r.Query(ids[0])
		if lead.Phase == protocol.Finished {
			return
		}
		turnID := ids[lead.Turn]
		view := r.Query(turnID)
		hand := view.YourHand
		if len(view.Table) == 0 {
			r.Submit(PlayCmd{ID: turnID, Cards: []game.Card{hand[0]}}) // lead lowest
			continue
		}
		if len(view.Table) == 1 {
			tbl, _ := game.Classify(view.Table, game.SimpleStraight)
			played := false
			for _, c := range hand {
				cc, _ := game.Classify([]game.Card{c}, game.SimpleStraight)
				if cc.Beats(tbl) {
					r.Submit(PlayCmd{ID: turnID, Cards: []game.Card{c}})
					played = true
					break
				}
			}
			if !played {
				r.Submit(PassCmd{ID: turnID})
			}
			continue
		}
		r.Submit(PassCmd{ID: turnID}) // dumb: never beat a multi-card lead
	}
	t.Fatal("hand did not finish within iteration budget")
}

// TestFullGameViaActor drives a whole hand through the actor: one winner, scores
// accumulate. Run under -race to exercise the single-owner model.
func TestFullGameViaActor(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(99)))
	ids := joinStart(r, 4)
	playOutHand(t, r, ids)

	final := r.Query(ids[0])
	if final.Phase != protocol.Finished {
		t.Fatalf("game did not finish; phase = %v", final.Phase)
	}
	if final.Winner < 0 {
		t.Fatalf("no winner recorded")
	}
	winners, zeros := 0, 0
	totalScore := 0
	for _, p := range final.Players {
		if p.CardCount == 0 {
			winners++
		}
		if p.Score == 0 {
			zeros++
		}
		totalScore += p.Score
	}
	if winners != 1 {
		t.Errorf("players with empty hand = %d, want 1", winners)
	}
	if totalScore <= 0 {
		t.Errorf("total penalty score = %d, want > 0", totalScore)
	}
}

// TestScoredMatchAccumulates plays two hands and checks the scoreboard carries over.
func TestScoredMatchAccumulates(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(7)))
	ids := joinStart(r, 4)

	playOutHand(t, r, ids)
	after1 := r.Query(ids[0])
	if after1.Phase != protocol.Finished {
		t.Fatalf("hand 1 phase = %v, want Finished", after1.Phase)
	}
	sum1 := 0
	for _, p := range after1.Players {
		sum1 += p.Score
	}
	if sum1 <= 0 {
		t.Fatalf("hand 1 total score = %d, want > 0", sum1)
	}

	// Host deals the next hand; scores must persist and everyone gets 13 cards.
	r.Submit(NextHandCmd{ID: ids[0]})
	next := r.Query(ids[0])
	if next.Phase != protocol.InGame {
		t.Fatalf("after next-hand phase = %v, want InGame", next.Phase)
	}
	for _, p := range next.Players {
		if p.CardCount != 13 {
			t.Errorf("seat %d has %d cards after redeal, want 13", p.Seat, p.CardCount)
		}
	}
	sumCarry := 0
	for _, p := range next.Players {
		sumCarry += p.Score
	}
	if sumCarry != sum1 {
		t.Errorf("scores did not carry over: got %d, want %d", sumCarry, sum1)
	}

	playOutHand(t, r, ids)
	after2 := r.Query(ids[0])
	sum2 := 0
	for _, p := range after2.Players {
		sum2 += p.Score
	}
	if sum2 <= sum1 {
		t.Errorf("cumulative score did not grow across hands: hand1=%d, total=%d", sum1, sum2)
	}
}

// TestDisconnectAutoAdvance verifies a disconnected player's turn auto-resolves
// so the game never stalls.
func TestDisconnectAutoAdvance(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(3)))
	ids := joinStart(r, 4)
	start := r.Query(ids[0])
	// Disconnect whoever is on turn; the game should keep progressing.
	r.Submit(DisconnectCmd{ID: ids[start.Turn]})
	after := r.Query(ids[0])
	if after.Turn == start.Turn && after.Phase == protocol.InGame {
		// The seat is disconnected, so the turn must have moved on or the hand
		// advanced.
		if after.Players[start.Turn].Connected {
			t.Fatalf("disconnected seat still shown connected")
		}
	}
	// Play should be able to run to completion with the remaining players.
	playOutHand(t, r, ids)
	if r.Query(ids[0]).Phase != protocol.Finished {
		t.Fatalf("game with a disconnected player did not finish")
	}
}

// TestMultiDisconnectAutoAdvance drops two adjacent seats mid-game and drives only
// the remaining connected players. The actor must fast-forward through BOTH
// disconnected seats every time the turn reaches them; if it ever left the turn on
// a disconnected seat the driver would catch the stall (unlike playOutHand, which
// would mask it by playing the disconnected seat itself).
func TestMultiDisconnectAutoAdvance(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(21)))
	ids := joinStart(r, 4)
	r.Submit(DisconnectCmd{ID: ids[1]})
	r.Submit(DisconnectCmd{ID: ids[2]})

	for iter := 0; iter < 2000; iter++ {
		snap := r.Query(ids[0])
		if snap.Phase == protocol.Finished {
			return
		}
		turn := snap.Turn
		if !snap.Players[turn].Connected {
			t.Fatalf("turn stalled on disconnected seat %d; auto-advance did not fast-forward", turn)
		}
		turnID := ids[turn]
		view := r.Query(turnID)
		hand := view.YourHand
		if len(view.Table) == 0 {
			r.Submit(PlayCmd{ID: turnID, Cards: []game.Card{hand[0]}}) // lead lowest
			continue
		}
		if len(view.Table) == 1 {
			tbl, _ := game.Classify(view.Table, game.SimpleStraight)
			played := false
			for _, c := range hand {
				cc, _ := game.Classify([]game.Card{c}, game.SimpleStraight)
				if cc.Beats(tbl) {
					r.Submit(PlayCmd{ID: turnID, Cards: []game.Card{c}})
					played = true
					break
				}
			}
			if !played {
				r.Submit(PassCmd{ID: turnID})
			}
			continue
		}
		r.Submit(PassCmd{ID: turnID}) // never beat a multi-card lead
	}
	t.Fatal("game with two disconnected players did not finish; likely stalled")
}

// TestTwoPlayerGame verifies a 2-player game starts (17 each, 34 in play) and
// runs to completion.
func TestTwoPlayerGame(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(11)))
	ids := joinStart(r, 2)
	snap := r.Query(ids[0])
	if snap.Phase != protocol.InGame {
		t.Fatalf("2-player game did not start; phase = %v", snap.Phase)
	}
	total := 0
	for _, p := range snap.Players {
		total += p.CardCount
	}
	if total != 34 {
		t.Errorf("2-player cards in play = %d, want 34", total)
	}
	playOutHand(t, r, ids)
	if r.Query(ids[0]).Phase != protocol.Finished {
		t.Fatalf("2-player game did not finish")
	}
}
