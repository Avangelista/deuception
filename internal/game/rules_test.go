package game

import (
	"errors"
	"testing"
)

// gameWithRules is gameWith with an explicit ruleset (which gameWith fixes to
// DefaultRules). It reproduces Deal's post-conditions.
func gameWithRules(t *testing.T, r Rules, hands ...string) *GameState {
	t.Helper()
	g := NewGame(len(hands), r)
	for i, h := range hands {
		g.Hands[i] = cards(t, h)
		sortCards(g.Hands[i])
	}
	g.OpenCard = g.lowestDealtCard()
	g.Turn = g.seatWithCard(g.OpenCard)
	g.Leader = g.Turn
	g.Table = nil
	g.Started = true
	g.firstPlay = true
	g.Winner = -1
	return g
}

// TestStraightStyleValidity pins which five-card sequences each style accepts as a
// straight and which it rejects as junk.
func TestStraightStyleValidity(t *testing.T) {
	const (
		low   = "3D 4C 5H 6S 7D" // 3-4-5-6-7
		mid   = "4D 5C 6H 7S 8D" // 4-5-6-7-8
		toA   = "TD JC QH KS AD" // 10-J-Q-K-A
		to2   = "JD QC KH AS 2D" // J-Q-K-A-2
		wheel = "AD 2C 3H 4S 5D" // A-2-3-4-5
		six   = "2D 3C 4H 5S 6D" // 2-3-4-5-6
	)
	tests := []struct {
		name  string
		style StraightStyle
		hand  string
		valid bool
	}{
		{"big2 low", StraightsBig2, low, true},
		{"big2 to ace", StraightsBig2, toA, true},
		{"big2 to two", StraightsBig2, to2, true},
		{"big2 no wheel", StraightsBig2, wheel, false},
		{"big2 no six-wrap", StraightsBig2, six, false},

		{"poker low", StraightsPoker, low, true},
		{"poker to ace", StraightsPoker, toA, true},
		{"poker wheel", StraightsPoker, wheel, true},
		{"poker six-wrap", StraightsPoker, six, true},
		{"poker no to-two", StraightsPoker, to2, false},

		{"hk low", StraightsHongKong, low, true},
		{"hk to ace", StraightsHongKong, toA, true},
		{"hk wheel", StraightsHongKong, wheel, true},
		{"hk six-wrap", StraightsHongKong, six, true},
		{"hk no to-two", StraightsHongKong, to2, false},
		{"hk mid", StraightsHongKong, mid, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Rules{Straights: tc.style}
			c, err := Classify(cards(t, tc.hand), r)
			if tc.valid {
				if err != nil {
					t.Fatalf("Classify(%q) err = %v, want a straight", tc.hand, err)
				}
				if c.Type != Straight {
					t.Errorf("Classify(%q).Type = %v, want Straight", tc.hand, c.Type)
				}
			} else if !errors.Is(err, ErrNoFiveCombo) {
				t.Errorf("Classify(%q) err = %v, want ErrNoFiveCombo", tc.hand, err)
			}
		})
	}
}

// TestStraightStyleRanking walks an ascending chain per style: every straight must
// beat all earlier ones and lose to all later ones.
func TestStraightStyleRanking(t *testing.T) {
	chains := []struct {
		name  string
		style StraightStyle
		asc   []string // strictly increasing strength
	}{
		{"big2", StraightsBig2, []string{
			"3D 4C 5H 6S 7D", // 34567
			"TD JC QH KS AD", // 10JQKA
			"JD QC KH AS 2D", // JQKA2 (highest)
		}},
		{"poker", StraightsPoker, []string{
			"AD 2C 3H 4S 5D", // wheel (lowest)
			"2D 3C 4H 5S 6D", // 23456
			"3D 4C 5H 6S 7D", // 34567
			"4D 5C 6H 7S 8D", // 45678
			"TD JC QH KS AD", // 10JQKA (highest)
		}},
		{"hongkong", StraightsHongKong, []string{
			"3D 4C 5H 6S 7D", // 34567 (lowest)
			"4D 5C 6H 7S 8D", // 45678
			"TD JC QH KS AD", // 10JQKA
			"2D 3C 4H 5S 6D", // 23456
			"AD 2C 3H 4S 5D", // A2345 (highest)
		}},
	}
	for _, ch := range chains {
		t.Run(ch.name, func(t *testing.T) {
			r := Rules{Straights: ch.style}
			combos := make([]Combo, len(ch.asc))
			for i, s := range ch.asc {
				c, err := Classify(cards(t, s), r)
				if err != nil {
					t.Fatalf("Classify(%q): %v", s, err)
				}
				combos[i] = c
			}
			for i := 0; i < len(combos); i++ {
				for j := i + 1; j < len(combos); j++ {
					if !combos[j].Beats(combos[i], r) {
						t.Errorf("%q should beat %q", ch.asc[j], ch.asc[i])
					}
					if combos[i].Beats(combos[j], r) {
						t.Errorf("%q should NOT beat %q", ch.asc[i], ch.asc[j])
					}
				}
			}
		})
	}
}

// TestFlushRanking contrasts the two flush ranking rules on the same pairs.
func TestFlushRanking(t *testing.T) {
	tests := []struct {
		name string
		rank FlushRank
		a, b string
		want bool
	}{
		// High-card: the higher top card wins, suit is only the final tiebreak.
		{"highcard higher top wins", FlushByHighCard, "4D 6D 8D TD KD", "3C 5C 7C 9C JC", true},
		{"highcard suit ignored below top", FlushByHighCard, "3C 5C 7C 9C JC", "4D 6D 8D TD KD", false},
		{"highcard suit breaks equal ranks", FlushByHighCard, "3H 5H 7H 9H KH", "3D 5D 7D 9D KD", true},

		// By-suit: the flush's suit dominates; card ranks only matter within a suit.
		{"bysuit club beats diamond", FlushBySuit, "3C 5C 7C 9C JC", "4D 6D 8D TD KD", true},
		{"bysuit diamond loses to club", FlushBySuit, "4D 6D 8D TD KD", "3C 5C 7C 9C JC", false},
		{"bysuit spade beats club", FlushBySuit, "3S 5S 7S 9S JS", "4C 6C 8C TC KC", true},
		{"bysuit same suit higher card wins", FlushBySuit, "4D 6D 8D TD KD", "3D 5D 7D 9D JD", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Rules{Flush: tc.rank}
			a, err := Classify(cards(t, tc.a), r)
			if err != nil {
				t.Fatalf("Classify(%q): %v", tc.a, err)
			}
			b, err := Classify(cards(t, tc.b), r)
			if err != nil {
				t.Fatalf("Classify(%q): %v", tc.b, err)
			}
			if got := a.Beats(b, r); got != tc.want {
				t.Errorf("(%q).Beats(%q) [%v] = %v, want %v", tc.a, tc.b, tc.rank, got, tc.want)
			}
		})
	}
}

// TestPassReenter verifies that under PassReenter a fresh play reopens the round: a
// player who passed earlier gets to act again on the same trick.
func TestPassReenter(t *testing.T) {
	g := gameWithRules(t, Rules{Pass: PassReenter},
		"3D 4D 5D",
		"3C 4C 5C",
		"3H 4H 5H",
	)
	mustPlay(t, g, 0, "3D") // s0 leads
	mustPass(t, g, 1)       // s1 skips this turn
	if !g.Passed[1] {
		t.Fatalf("Passed[1] should be set right after s1 passes")
	}
	mustPlay(t, g, 2, "3H") // a fresh play reopens the round
	if g.Passed[1] {
		t.Fatalf("PassReenter: s1's pass should be cleared by a later play")
	}
	// The turn cycles back around and s1, having re-entered, can play again.
	mustPlay(t, g, 0, "4D") // s0 re-enters too and beats 3H
	if g.Turn != 1 {
		t.Fatalf("Turn = %d, want 1 (s1 re-entered)", g.Turn)
	}
	if _, err := g.Play(1, cards(t, "4C")); err != nil {
		t.Fatalf("s1 should be able to play again after re-entering: %v", err)
	}
}

// TestLeadWinner verifies LeadFrom hands a free opening play to an arbitrary seat: no
// open-card requirement, and that seat leads.
func TestLeadWinner(t *testing.T) {
	g := gameWithRules(t, Rules{Lead: LeadWinner},
		"3D 4D 5D",
		"3C 4C 5C",
		"5H 6H 7H",
		"3S 4S 5S",
	)
	g.LeadFrom(2)
	if g.Turn != 2 || g.Leader != 2 {
		t.Fatalf("after LeadFrom(2): Turn=%d Leader=%d, want 2/2", g.Turn, g.Leader)
	}
	if g.FirstPlay() {
		t.Fatalf("LeadFrom should clear firstPlay so the open-card rule is off")
	}
	// s2 opens freely with a card that is not the game's OpenCard (3D).
	if _, err := g.Play(2, cards(t, "5H")); err != nil {
		t.Fatalf("winner should open freely with any card: %v", err)
	}
	if g.Turn != 3 {
		t.Fatalf("after s2 opens, Turn=%d, want 3", g.Turn)
	}
}

// TestSanitized clamps out-of-range fields (from an untrusted prefs file) to defaults
// while leaving valid rulesets untouched.
func TestSanitized(t *testing.T) {
	valid := Rules{Straights: StraightsHongKong, Flush: FlushBySuit, Pass: PassReenter, Lead: LeadWinner}
	if got := valid.Sanitized(); got != valid {
		t.Errorf("valid ruleset changed by Sanitized: %+v", got)
	}
	bad := Rules{Straights: 200, Flush: 9, Pass: 3, Lead: 7}
	if got := bad.Sanitized(); got != (Rules{}) {
		t.Errorf("out-of-range ruleset = %+v, want all defaults", got)
	}
}

// TestDefaultRulesIsClassic guards the invariant the whole refactor rests on: the zero
// value reproduces the classic straight and pass behaviour.
func TestDefaultRulesIsClassic(t *testing.T) {
	if DefaultRules() != (Rules{}) {
		t.Fatalf("DefaultRules() = %+v, want zero value", DefaultRules())
	}
	// Classic straights: JQKA2 legal, wheel illegal.
	if _, err := Classify(cards(t, "JD QC KH AS 2D"), DefaultRules()); err != nil {
		t.Errorf("classic should accept J-Q-K-A-2: %v", err)
	}
	if _, err := Classify(cards(t, "AD 2C 3H 4S 5D"), DefaultRules()); !errors.Is(err, ErrNoFiveCombo) {
		t.Errorf("classic should reject the wheel: err = %v", err)
	}
}
