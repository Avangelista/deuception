package game

import "sort"

// StraightStyle selects which five-card sequences count as straights and how they rank.
type StraightStyle uint8

const (
	// StraightsBig2: 2 is the top rank, so J-Q-K-A-2 is the highest straight and there
	// is no ace-low wrap. The classic behaviour.
	StraightsBig2 StraightStyle = iota
	// StraightsPoker: poker sequences - the ace-low wheel A-2-3-4-5 is the lowest,
	// 10-J-Q-K-A the highest; the 2 counts low; J-Q-K-A-2 is not a straight.
	StraightsPoker
	// StraightsHongKong: the same legal set as Poker, but the two 2-wraps rank highest:
	// A-2-3-4-5 tops everything, then 2-3-4-5-6, then 10-J-Q-K-A down to 3-4-5-6-7.
	StraightsHongKong
)

// FlushRank selects how two flushes compare.
type FlushRank uint8

const (
	FlushByHighCard FlushRank = iota // highest card down, then the top card's suit
	FlushBySuit                      // the flush's suit first, then highest card down
)

// PassRule selects passing behaviour.
type PassRule uint8

const (
	PassLockout PassRule = iota // once you pass you are out until the trick resets
	PassReenter                 // a pass only skips this turn; a later play reopens it
)

// LeadRule selects who opens each hand.
type LeadRule uint8

const (
	LeadLowCard LeadRule = iota // the lowest dealt card (the 3D) opens, and must be played
	LeadWinner                  // the previous hand's winner opens freely (hand 1 still 3D)
)

// Rules is the configurable house-rules set for a match. The zero value is the classic
// game (Big 2 straights, high-card flushes, lockout passing, 3D opens).
type Rules struct {
	Straights StraightStyle
	Flush     FlushRank
	Pass      PassRule
	Lead      LeadRule
}

// DefaultRules is the classic ruleset (all zero values).
func DefaultRules() Rules { return Rules{} }

// Sanitized resets any out-of-range field to its default, so a ruleset read from an
// untrusted source (a hand-edited prefs file) can't index past an option list. The
// fields are unsigned, so only the upper bound needs checking.
func (r Rules) Sanitized() Rules {
	if r.Straights > StraightsHongKong {
		r.Straights = StraightsBig2
	}
	if r.Flush > FlushBySuit {
		r.Flush = FlushByHighCard
	}
	if r.Pass > PassReenter {
		r.Pass = PassLockout
	}
	if r.Lead > LeadWinner {
		r.Lead = LeadLowCard
	}
	return r
}

// straight reports whether five Order-sorted cards form a straight under the style,
// returning a comparison rank (higher beats lower, within a style) and the card whose
// suit breaks ties.
func (r Rules) straight(sorted []Card) (ok bool, srank int, high Card) {
	switch r.Straights {
	case StraightsPoker:
		return pokerStraight(sorted, false)
	case StraightsHongKong:
		return pokerStraight(sorted, true)
	default:
		return big2Straight(sorted)
	}
}

// big2Straight: five consecutive ranks in Big 2 order (2 highest). Ranks by high card.
func big2Straight(sorted []Card) (bool, int, Card) {
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Rank != sorted[i-1].Rank+1 {
			return false, 0, Card{}
		}
	}
	high := sorted[len(sorted)-1]
	return true, int(high.Rank), high
}

// pokerVal maps a rank to its poker value (2 low, ace high by default).
func pokerVal(r Rank) int {
	if r == Rank2 {
		return 2
	}
	return int(r) + 3 // Rank3(0)->3 ... RankA(11)->14
}

// pokerStraight validates a poker sequence (ace high or low, 2 always low, no wrap past
// the 2) and returns a comparison srank plus the top card for suit tiebreaks. hk switches
// to the Hong Kong ranking, where the two 2-wraps outrank every normal straight. Because
// the ace only ever goes low in the wheel, the top card of any run is never the ace, so
// pokerVal(top.Rank) is its comparison value in both the ace-high and ace-low cases.
func pokerStraight(sorted []Card, hk bool) (bool, int, Card) {
	type cv struct {
		v int
		c Card
	}
	hasAce := false
	for _, c := range sorted {
		if c.Rank == RankA {
			hasAce = true
		}
	}
	run := func(aceLow bool) (bool, Card) {
		vs := make([]cv, len(sorted))
		for i, c := range sorted {
			v := pokerVal(c.Rank)
			if aceLow && c.Rank == RankA {
				v = 1
			}
			vs[i] = cv{v, c}
		}
		sort.Slice(vs, func(i, j int) bool { return vs[i].v < vs[j].v })
		for i := 1; i < len(vs); i++ {
			if vs[i].v != vs[i-1].v+1 {
				return false, Card{}
			}
		}
		return true, vs[len(vs)-1].c // the poker-top card (never the ace when aceLow)
	}
	for _, aceLow := range []bool{false, true} {
		if aceLow && !hasAce {
			continue
		}
		if ok, top := run(aceLow); ok {
			return true, hkSRank(sorted, pokerVal(top.Rank), hk), top
		}
	}
	return false, 0, Card{}
}

// hkSRank returns the straight's comparison rank: the top poker value for Poker, or, for
// Hong Kong, a high sentinel for the two 2-wraps so they outrank every normal straight.
func hkSRank(sorted []Card, top int, hk bool) int {
	if !hk {
		return top
	}
	has2, hasA := false, false
	for _, c := range sorted {
		switch c.Rank {
		case Rank2:
			has2 = true
		case RankA:
			hasA = true
		}
	}
	switch {
	case has2 && hasA:
		return 200 // A-2-3-4-5, the top straight
	case has2:
		return 199 // 2-3-4-5-6
	default:
		return top // normal straights, 7..14
	}
}

// flushBeats reports whether flush a beats flush b under the ranking rule. Both slices
// are Order-sorted, five same-suit cards.
func flushBeats(a, b []Card, rank FlushRank) bool {
	if rank == FlushBySuit {
		if a[4].Suit != b[4].Suit {
			return a[4].Suit > b[4].Suit // the flush's (shared) suit
		}
	}
	for i := 4; i >= 0; i-- { // highest card down
		if a[i].Rank != b[i].Rank {
			return a[i].Rank > b[i].Rank
		}
	}
	return a[4].Suit > b[4].Suit // top card's suit (matters only for FlushByHighCard)
}
