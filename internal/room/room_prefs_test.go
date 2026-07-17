package room

import (
	mrand "math/rand"
	"sync"
	"testing"

	"github.com/Avangelista/big2-tui/internal/game"
	"github.com/Avangelista/big2-tui/internal/protocol"
)

// capturePersister records the latest preferences the room asked to save.
type capturePersister struct {
	mu        sync.Mutex
	calls     int
	rules     game.Rules
	reactions []string
	letters   map[string]string
}

func (c *capturePersister) Save(rules game.Rules, reactions []string, letters map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.rules = rules
	c.reactions = append([]string(nil), reactions...)
	c.letters = map[string]string{}
	for k, v := range letters {
		c.letters[k] = v
	}
}

func (c *capturePersister) snapshot() (int, game.Rules, map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.rules, c.letters
}

func TestPersistOnRulesChange(t *testing.T) {
	cp := &capturePersister{}
	r := New(4, 3, mrand.New(mrand.NewSource(1)), WithPersister(cp))
	ids := joinN(r, 3)

	if calls, _, _ := cp.snapshot(); calls != 0 {
		t.Fatalf("joining should not persist yet, got %d saves", calls)
	}
	want := game.Rules{Straights: game.StraightsPoker, Pass: game.PassReenter}
	r.Submit(SetRulesCmd{ID: ids[0], Rules: want})
	r.Query(ids[0]) // serialize: the save has run by the time this returns
	calls, rules, _ := cp.snapshot()
	if calls == 0 {
		t.Fatal("changing rules should persist")
	}
	if rules != want {
		t.Errorf("persisted rules = %+v, want %+v", rules, want)
	}
}

func TestPersistLetterOnPick(t *testing.T) {
	cp := &capturePersister{}
	r := New(4, 3, mrand.New(mrand.NewSource(1)), WithPersister(cp))
	id := NewID()
	r.Submit(JoinCmd{ID: id, Identity: "SHA256:abc", Host: true})
	r.Submit(SetLetterCmd{ID: id, Letter: 'Q'})
	snap := r.Query(id)
	if got := snap.Players[snap.YouSeat].Letter; got != 'Q' {
		t.Fatalf("seat letter = %c, want Q", got)
	}
	_, _, letters := cp.snapshot()
	if letters["SHA256:abc"] != "Q" {
		t.Errorf("picked letter not persisted under identity: %v", letters)
	}
}

func TestPersistLetterOnRepick(t *testing.T) {
	cp := &capturePersister{}
	r := New(4, 3, mrand.New(mrand.NewSource(1)), WithPersister(cp))
	id := NewID()
	r.Submit(JoinCmd{ID: id, Identity: "me", Host: true})
	cur := r.Query(id).Players[0].Letter // the auto-assigned letter
	// Re-pick the SAME letter: no change to the seat, but the explicit pick is remembered.
	r.Submit(SetLetterCmd{ID: id, Letter: cur})
	r.Query(id)
	if _, _, letters := cp.snapshot(); letters["me"] != string(cur) {
		t.Errorf("re-picking the current letter should persist it: got %v, want %c", letters, cur)
	}
}

func TestRestoreLetterBumpsBot(t *testing.T) {
	// seed 15: the first bot added lands on 'Q' (randomFreeLetter over B..Z), so a
	// returning player whose saved letter is 'Q' must bump it, like a manual pick would.
	r := New(4, 3, mrand.New(mrand.NewSource(15)),
		WithSavedPrefs(game.Rules{}, nil, map[string]string{"host": "A", "me": "Q"}))
	h := NewID()
	r.Submit(JoinCmd{ID: h, Identity: "host", Host: true})
	r.Submit(AddBotCmd{ID: h})
	if got := r.Query(h).Players[1].Letter; got != 'Q' { // guard against seed drift
		t.Fatalf("setup: bot letter = %c, want Q", got)
	}
	me := NewID()
	r.Submit(JoinCmd{ID: me, Identity: "me"})
	snap := r.Query(me)
	if got := snap.Players[2].Letter; got != 'Q' {
		t.Errorf("returning player should reclaim Q from the bot, got %c", got)
	}
	if got := snap.Players[1].Letter; got == 'Q' {
		t.Errorf("bot should have been bumped off Q, still has %c", got)
	}
}

func TestNoIdentityNotRemembered(t *testing.T) {
	cp := &capturePersister{}
	r := New(4, 3, mrand.New(mrand.NewSource(1)), WithPersister(cp))
	id := NewID()
	r.Submit(JoinCmd{ID: id, Host: true}) // no identity
	r.Submit(SetLetterCmd{ID: id, Letter: 'Q'})
	r.Query(id)
	if calls, _, letters := cp.snapshot(); calls != 0 || len(letters) != 0 {
		t.Errorf("a letter pick with no identity should not persist: calls=%d letters=%v", calls, letters)
	}
}

func TestRestoreLetterOnJoin(t *testing.T) {
	r := New(4, 3, mrand.New(mrand.NewSource(1)),
		WithSavedPrefs(game.Rules{}, nil, map[string]string{"SHA256:abc": "Q"}))
	id := NewID()
	r.Submit(JoinCmd{ID: id, Identity: "SHA256:abc", Host: true})
	snap := r.Query(id)
	if got := snap.Players[snap.YouSeat].Letter; got != 'Q' {
		t.Errorf("restored letter = %c, want Q", got)
	}
}

func TestRestoreLetterSkippedWhenTaken(t *testing.T) {
	// Two identities both saved 'Q'; the second joiner can't reuse it and gets another.
	r := New(4, 3, mrand.New(mrand.NewSource(1)),
		WithSavedPrefs(game.Rules{}, nil, map[string]string{"one": "Q", "two": "Q"}))
	a, b := NewID(), NewID()
	r.Submit(JoinCmd{ID: a, Identity: "one", Host: true})
	r.Submit(JoinCmd{ID: b, Identity: "two"})
	snap := r.Query(b)
	la := snap.Players[0].Letter
	lb := snap.Players[1].Letter
	if la != 'Q' {
		t.Errorf("first joiner should get its saved Q, got %c", la)
	}
	if lb == la {
		t.Errorf("second joiner reused a taken letter %c", lb)
	}
}

func TestWithSavedPrefsSeedsAndSanitizes(t *testing.T) {
	bad := protocol.DefaultReactions()
	bad[1] = "waytoolong" // > MaxReactionLen -> falls back to default
	bad[2] = "   "        // blank -> falls back to default
	bad[3] = "ok"         // valid -> kept
	r := New(4, 3, mrand.New(mrand.NewSource(1)),
		WithSavedPrefs(game.Rules{Straights: 99, Flush: game.FlushBySuit}, bad, nil))
	id := NewID()
	r.Submit(JoinCmd{ID: id, Host: true})
	snap := r.Query(id)
	if snap.Rules.Straights != game.StraightsBig2 {
		t.Errorf("out-of-range straight not sanitized: %v", snap.Rules.Straights)
	}
	if snap.Rules.Flush != game.FlushBySuit {
		t.Errorf("valid flush rule lost: %v", snap.Rules.Flush)
	}
	if snap.Reactions[1] != protocol.Emotes[1] || snap.Reactions[2] != protocol.Emotes[2] {
		t.Errorf("bad labels not sanitized: %q %q", snap.Reactions[1], snap.Reactions[2])
	}
	if snap.Reactions[3] != "ok" {
		t.Errorf("valid label not kept: %q", snap.Reactions[3])
	}
}
