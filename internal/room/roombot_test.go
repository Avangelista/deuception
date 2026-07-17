package room

import (
	mrand "math/rand"
	"testing"
	"time"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
)

// TestBotActStaleTokenIgnored: a BotActCmd whose Token no longer matches the room's
// turnToken must be a no-op even when its seat/turn are correct, so a late timer
// can't double-play. botDelay is frozen so we inject the command deterministically.
func TestBotActStaleTokenIgnored(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(5)))
	r.botDelay = time.Hour // freeze the real scheduler; we inject BotActCmds ourselves

	host := NewID()
	r.Submit(JoinCmd{ID: host, Host: true})
	r.Submit(AddBotCmd{ID: host})
	r.Submit(AddBotCmd{ID: host})
	r.Submit(StartCmd{ID: host})

	snap := r.Query(host)
	if snap.Phase != protocol.InGame {
		t.Fatalf("game did not start: %v", snap.Phase)
	}
	// If the human opens, play the opener (lowest card, a legal single) so the turn
	// lands on a bot seat.
	if snap.Turn == snap.YouSeat {
		r.Submit(PlayCmd{ID: host, Cards: snap.YourHand[:1]})
		snap = r.Query(host)
	}
	botSeat := snap.Turn
	if !snap.Players[botSeat].IsBot {
		t.Fatalf("expected a bot on turn, seat %d is not a bot", botSeat)
	}

	before := r.Query(host)
	// turnToken only ever increments from 0, so -1 can never be current.
	r.Submit(BotActCmd{Seat: botSeat, Token: -1})
	after := r.Query(host)

	if after.Turn != before.Turn {
		t.Fatalf("stale BotActCmd advanced the turn: %d -> %d", before.Turn, after.Turn)
	}
	for i := range before.Players {
		if before.Players[i].CardCount != after.Players[i].CardCount {
			t.Fatalf("stale BotActCmd changed seat %d hand: %d -> %d",
				i, before.Players[i].CardCount, after.Players[i].CardCount)
		}
	}
}

// TestTrickWonHold: a pass that wins a trick holds the completed trick on screen -
// the winning card, the final pass X, and no active turn - until it clears.
func TestTrickWonHold(t *testing.T) {
	r := New(2, 2, mrand.New(mrand.NewSource(11)))
	r.trickDelay = time.Hour // hold indefinitely so we can inspect it deterministically

	a, b := NewID(), NewID()
	r.Submit(JoinCmd{ID: a, Host: true})
	r.Submit(JoinCmd{ID: b})
	r.Submit(StartCmd{ID: a})

	ids := []string{a, b}
	opener := r.Query(a).Turn
	openerID, otherID := ids[opener], ids[1-opener]

	// Opener leads its lowest card (the open card); the other passes, which in a
	// 2-player game wins the trick.
	r.Submit(PlayCmd{ID: openerID, Cards: r.Query(openerID).YourHand[:1]})
	r.Submit(PassCmd{ID: otherID})

	rev := r.Query(a)
	if rev.Turn != -1 {
		t.Fatalf("held trick should have no active turn, got Turn=%d", rev.Turn)
	}
	if len(rev.Table) == 0 {
		t.Fatal("held trick should still show the winning card")
	}
	if !rev.Players[1-opener].Passed {
		t.Fatal("the final passer's X should show during the hold")
	}
	if rev.Players[opener].IsTurn {
		t.Fatal("no player should be on turn during the hold")
	}

	// The winner leading a fresh card ends the hold and resumes normal play.
	r.Submit(PlayCmd{ID: openerID, Cards: r.Query(openerID).YourHand[:1]})
	if after := r.Query(a); after.Turn == -1 {
		t.Fatal("hold should have ended once the winner led a new card")
	}
}

// TestDisconnectedTrickWinHeld: when a play wins a trick because the remaining
// opponents are disconnected and auto-pass, the completed trick is held on screen
// (the winning card, no active turn) rather than collapsing straight to the reset -
// so the play still animates on every client.
func TestDisconnectedTrickWinHeld(t *testing.T) {
	r := New(2, 2, mrand.New(mrand.NewSource(3)))
	r.trickDelay = time.Hour // hold so we can inspect the reveal deterministically

	a, b := NewID(), NewID()
	r.Submit(JoinCmd{ID: a, Host: true})
	r.Submit(JoinCmd{ID: b})
	r.Submit(StartCmd{ID: a})
	r.Submit(DisconnectCmd{ID: b}) // b drops; its turns now auto-pass/lead

	for i := 0; i < 60; i++ {
		snap := r.Query(a)
		if snap.Phase != protocol.InGame {
			t.Fatal("game ended before a trick was held")
		}
		if snap.Turn == -1 { // a trick is being held (reveal)
			if len(snap.Table) == 0 {
				t.Fatal("held trick should still show the winning card")
			}
			return // success: the disconnected trick win was held, not collapsed
		}
		if snap.Turn != 0 {
			t.Fatalf("turn %d rests on the disconnected seat; auto-advance failed", snap.Turn)
		}
		hand := r.Query(a).YourHand
		if len(hand) == 0 {
			t.Fatal("a ran out of cards before a trick was held")
		}
		if len(snap.Table) == 0 {
			r.Submit(PlayCmd{ID: a, Cards: []game.Card{hand[0]}}) // lead lowest
			continue
		}
		played := false
		if len(snap.Table) == 1 {
			tbl, _ := game.Classify(snap.Table, game.SimpleStraight)
			for _, c := range hand {
				if cc, _ := game.Classify([]game.Card{c}, game.SimpleStraight); cc.Beats(tbl) {
					r.Submit(PlayCmd{ID: a, Cards: []game.Card{c}})
					played = true
					break
				}
			}
		}
		if !played {
			r.Submit(PassCmd{ID: a})
		}
	}
	t.Fatal("expected a held disconnected-trick reveal within the budget")
}

// TestHostLeavesLobbyPromotes: when the host leaves the waiting room, a remaining
// player is promoted so the room stays startable (regression: serve-only orphan).
func TestHostLeavesLobbyPromotes(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(9)))
	// Mirror serve-only: nobody claims Host, so the first joiner becomes host.
	a, b, c := NewID(), NewID(), NewID()
	r.Submit(JoinCmd{ID: a})
	r.Submit(JoinCmd{ID: b})
	r.Submit(JoinCmd{ID: c})
	if !r.Query(a).IsHost {
		t.Fatal("first joiner should be host")
	}

	r.Submit(DisconnectCmd{ID: a}) // host leaves the lobby

	// Exactly one of the remaining seats must now be host, and it can start the game.
	sb, sc := r.Query(b), r.Query(c)
	hosts := 0
	for _, p := range sb.Players {
		if p.IsHost {
			hosts++
		}
	}
	if hosts != 1 {
		t.Fatalf("after host left, host count = %d, want 1", hosts)
	}
	newHostID := b
	if !sb.IsHost {
		newHostID = c
		if !sc.IsHost {
			t.Fatal("no remaining seat was promoted to host")
		}
	}
	r.Submit(StartCmd{ID: newHostID})
	if got := r.Query(newHostID).Phase; got != protocol.InGame {
		t.Fatalf("promoted host could not start the game; phase = %v", got)
	}
}

func countBots(s protocol.StateSnapshot) int {
	n := 0
	for _, p := range s.Players {
		if p.IsBot {
			n++
		}
	}
	return n
}

// TestAddAndRemoveBot covers the host-only add/remove and that a fresh bot gets a
// unique in-range letter.
func TestAddAndRemoveBot(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(1)))
	host := NewID()
	r.Submit(JoinCmd{ID: host, Host: true})
	r.Submit(AddBotCmd{ID: host})

	snap := r.Query(host)
	if len(snap.Players) != 2 {
		t.Fatalf("players after add = %d, want 2", len(snap.Players))
	}
	b := snap.Players[1]
	if !b.IsBot {
		t.Fatalf("seat 1: IsBot=%v, want a bot", b.IsBot)
	}
	if b.Letter == snap.Players[0].Letter || b.Letter < 'A' || b.Letter > 'Z' {
		t.Fatalf("bot letter %q collides or is out of range", b.Letter)
	}

	// A non-host may neither add nor remove bots.
	p2 := NewID()
	r.Submit(JoinCmd{ID: p2})
	r.Submit(AddBotCmd{ID: p2})
	if n := len(r.Query(host).Players); n != 3 {
		t.Fatalf("non-host add changed the table (players=%d, want 3)", n)
	}
	r.Submit(RemoveBotCmd{ID: p2})
	if bots := countBots(r.Query(host)); bots != 1 {
		t.Fatalf("non-host remove changed the table (bots=%d, want 1)", bots)
	}

	// The host removes the bot.
	r.Submit(RemoveBotCmd{ID: host})
	if bots := countBots(r.Query(host)); bots != 0 {
		t.Fatalf("bots after host remove = %d, want 0", bots)
	}
}

// TestLetterClaimAndReject: letters default unique, a claim takes a free letter,
// and one human cannot steal another human's letter.
func TestLetterClaimAndReject(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(2)))
	host := NewID()
	r.Submit(JoinCmd{ID: host, Host: true})
	p2 := NewID()
	r.Submit(JoinCmd{ID: p2})

	snap := r.Query(host)
	if snap.Players[0].Letter == snap.Players[1].Letter {
		t.Fatalf("default letters collide (%c)", snap.Players[0].Letter)
	}

	r.Submit(SetLetterCmd{ID: host, Letter: 'z'}) // lower-case is upcased server-side
	if got := r.Query(host).Players[0].Letter; got != 'Z' {
		t.Fatalf("host letter = %c, want Z", got)
	}

	before := r.Query(p2).Players[1].Letter
	r.Submit(SetLetterCmd{ID: p2, Letter: 'Z'}) // held by a human: rejected
	after := r.Query(p2).Players[1].Letter
	if after == 'Z' || after != before {
		t.Fatalf("rejected claim changed p2 letter to %c (was %c)", after, before)
	}
}

// TestHumanBumpsBotLetter: a human always wins a contested letter; a bot holding
// it is bumped to a new free letter.
func TestHumanBumpsBotLetter(t *testing.T) {
	r := New(4, 2, mrand.New(mrand.NewSource(3)))
	host := NewID()
	r.Submit(JoinCmd{ID: host, Host: true})
	r.Submit(AddBotCmd{ID: host})

	snap := r.Query(host)
	if !snap.Players[1].IsBot {
		t.Fatal("seat 1 is not the bot")
	}
	botLetter := snap.Players[1].Letter

	r.Submit(SetLetterCmd{ID: host, Letter: botLetter})
	snap = r.Query(host)
	if snap.Players[0].Letter != botLetter {
		t.Fatalf("host did not take the bot's letter %c (got %c)", botLetter, snap.Players[0].Letter)
	}
	if snap.Players[1].Letter == botLetter {
		t.Fatal("bot kept its letter after being bumped")
	}
	if snap.Players[0].Letter == snap.Players[1].Letter {
		t.Fatal("letters collide after the bump")
	}
}

// humanDumbMove submits a guaranteed-legal move for a human seat: lead the lowest
// card, beat a single if possible, otherwise pass (mirrors playOutHand).
func humanDumbMove(r *Room, id string, snap protocol.StateSnapshot) {
	hand := snap.YourHand
	if len(hand) == 0 {
		return
	}
	if len(snap.Table) == 0 {
		r.Submit(PlayCmd{ID: id, Cards: []game.Card{hand[0]}})
		return
	}
	if len(snap.Table) == 1 {
		tbl, _ := game.Classify(snap.Table, game.SimpleStraight)
		for _, c := range hand {
			cc, _ := game.Classify([]game.Card{c}, game.SimpleStraight)
			if cc.Beats(tbl) {
				r.Submit(PlayCmd{ID: id, Cards: []game.Card{c}})
				return
			}
		}
	}
	r.Submit(PassCmd{ID: id})
}

// TestBotPlaysThroughActor drives a whole hand with one human and three bots. The
// bots resolve their own turns via the scheduler (botDelay 0 for speed); the game
// must reach a winner without stalling. Run under -race to exercise the scheduler
// goroutines against the single-owner actor.
func TestBotPlaysThroughActor(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(5)))
	r.botDelay = 0   // no artificial think time in tests
	r.trickDelay = 0 // no trick-won hold in tests

	host := NewID()
	r.Submit(JoinCmd{ID: host, Host: true})
	for i := 0; i < 3; i++ {
		r.Submit(AddBotCmd{ID: host})
	}
	r.Submit(StartCmd{ID: host})

	deadline := time.Now().Add(15 * time.Second)
	for {
		snap := r.Query(host)
		if snap.Phase == protocol.Finished {
			if snap.Winner < 0 {
				t.Fatal("game finished with no winner")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bot-driven game stalled; phase=%v turn=%d", snap.Phase, snap.Turn)
		}
		if snap.Phase == protocol.InGame && snap.Turn == snap.YouSeat {
			humanDumbMove(r, host, snap)
		} else {
			time.Sleep(time.Millisecond) // let scheduled bot moves land
		}
	}
}
