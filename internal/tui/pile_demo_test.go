package tui

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Avangelista/deuception/internal/game"
)

// colourModel builds a bare Model with a forced-colour renderer for style tests.
func colourModel() *Model {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	return &Model{r: r, st: newStyles(r), selected: map[int]bool{}}
}

var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripStyling removes SGR colour escapes and VS15 so a rendered frame can be
// matched by its plain composited glyphs.
func stripStyling(s string) string {
	return strings.ReplaceAll(sgrRe.ReplaceAllString(s, ""), vs15, "")
}

// pileFace is the composited left-border+rank+suit for a token, e.g. "6H" -> "│6♥".
func pileFace(tok string) string {
	c := card(tok)
	return "│" + string(c.Rank.Rune()) + string(c.Suit.Glyph())
}

// pileRowOf returns the topmost frame row showing tok's face, or -1.
func pileRowOf(frame, tok string) int {
	need := pileFace(tok)
	for r, line := range strings.Split(frame, "\n") {
		if strings.Contains(stripStyling(line), need) {
			return r
		}
	}
	return -1
}

// pileColOf returns the leftmost display column (rune index) of tok's face, or -1.
func pileColOf(frame, tok string) int {
	need := pileFace(tok)
	best := -1
	for _, line := range strings.Split(frame, "\n") {
		s := stripStyling(line)
		if i := strings.Index(s, need); i >= 0 {
			if col := len([]rune(s[:i])); best == -1 || col < best {
				best = col
			}
		}
	}
	return best
}

func card(s string) game.Card {
	c, err := game.ParseCard(s)
	if err != nil {
		panic(err)
	}
	return c
}
func cards(toks ...string) []game.Card {
	out := make([]game.Card, len(toks))
	for i, t := range toks {
		out[i] = card(t)
	}
	return out
}

// TestPileBoxMatchesDemo checks the rounded per-card pile against demo.txt exactly.
func TestPileBoxMatchesDemo(t *testing.T) {
	m := &Model{} // glyph mode (asciiSuits false)
	cases := []struct {
		name string
		cs   []game.Card
		want []string
	}{
		{"single", cards("2S"), []string{
			"╭────╮",
			"│2♠  │",
			"│    │",
			"╰────╯",
		}},
		{"pair", cards("4D", "4H"), []string{
			"╭──╭────╮",
			"│4♦│4♥  │",
			"│  │    │",
			"╰──╰────╯",
		}},
		{"straight", cards("5D", "6C", "7D", "8H", "9S"), []string{
			"╭──╭──╭──╭──╭────╮",
			"│5♦│6♣│7♦│8♥│9♠  │",
			"│  │  │  │  │    │",
			"╰──╰──╰──╰──╰────╯",
		}},
	}
	for _, tc := range cases {
		got := m.pileBoxLines(tc.cs)
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got:\n%s\nwant:\n%s", tc.name,
				strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
		}
	}
}

// TestSelfFanRoundedTiles checks the fan draws each card as a plain rounded tile,
// with a selected card popping up a row and no ┤/┴ joiners. Hand 4♦ 7♣ 9♥ J♠ 2♠,
// cursor on 7♣ (index 1), 9♥ (index 2) selected/lifted.
func TestSelfFanRoundedTiles(t *testing.T) {
	m := &Model{selected: map[int]bool{2: true}} // glyph mode
	hand := cards("4D", "7C", "9H", "JS", "2S")
	rows, _ := m.selfFan(hand, 0, len(hand), 1, true)
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = string(r)
	}
	want := []string{
		"      ╭────╮      ",
		"╭──╭──│9♥╭──╭────╮",
		"│4♦│7♣│  │J♠│2♠  │",
		"│  │* ╰──│  │    │",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("self-fan mismatch:\n got:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestHFanMatchesDemo2 checks the top opponent's back matches demo2.txt: a ░-filled
// body and a rounded floor, wide front card leftmost, each card keeping its ╯ corner.
func TestHFanMatchesDemo2(t *testing.T) {
	// On turn: spaced ░ checker body over the floor.
	fill, floor := hFan(4, 80, true)
	if fill != "│ ░░ │░ │░ │░ │" {
		t.Errorf("on-turn fill = %q", fill)
	}
	if floor != "╰────╯──╯──╯──╯" {
		t.Errorf("on-turn floor = %q", floor)
	}
	// Off turn: same floor, empty fill.
	fill, floor = hFan(4, 80, false)
	if fill != "" {
		t.Errorf("off-turn fill should be empty, got %q", fill)
	}
	if floor != "╰────╯──╯──╯──╯" {
		t.Errorf("off-turn floor = %q", floor)
	}
}

// TestSideFansMatchDemo2 pins the side backs to demo2.txt: 2-row slivers around a
// 4-row front card - left front at the bottom (slivers above), right front at the
// top (slivers below) - opening ──/░ toward the centre on their turn.
func TestSideFansMatchDemo2(t *testing.T) {
	// count 5, budget 12 -> 4 slivers + front.
	if got := vFanLeft(5, 12, false); strings.Join(got, ",") !=
		"╮,│,╮,│,╮,│,╮,│,╮,│,│,╯" {
		t.Errorf("left off = %q", got)
	}
	if got := vFanLeft(5, 12, true); strings.Join(got, ",") !=
		"──╮,░ │,──╮,░ │,──╮,░ │,──╮,░ │,──╮,░ │,░ │,──╯" {
		t.Errorf("left on = %q", got)
	}
	if got := vFanRight(5, 12, false); strings.Join(got, ",") !=
		"╭,│,│,╰,│,╰,│,╰,│,╰,│,╰" {
		t.Errorf("right off = %q", got)
	}
	if got := vFanRight(5, 12, true); strings.Join(got, ",") !=
		"╭──,│ ░,│ ░,╰──,│ ░,╰──,│ ░,╰──,│ ░,╰──,│ ░,╰──" {
		t.Errorf("right on = %q", got)
	}
}

// TestFaceColour checks a red card's rank+suit are both coloured red while a black
// card's face and all borders stay plain (no cursor/selected outline colour).
func TestFaceColour(t *testing.T) {
	m := colourModel()
	const red = "\x1b[31m"
	// Pile: red rank and pip go red together; black card and borders stay plain.
	got := m.paintPileRow([]rune("│4♥│5♠  │"))
	if !strings.Contains(got, red+"4♥") {
		t.Errorf("red card rank+suit should be red: %q", got)
	}
	if strings.Count(got, red) != 1 { // only the one red card, not the black one or borders
		t.Errorf("exactly one red run expected: %q", got)
	}
	// Self-fan: same rule, and no cyan/yellow highlight anywhere.
	m.selected = map[int]bool{0: true}
	rows, tags := m.selfFan(cards("4H", "5S"), 0, 2, 0, true)
	var painted strings.Builder
	for i := range rows {
		painted.WriteString(m.paintTagged(rows[i], tags[i]))
	}
	out := painted.String()
	if !strings.Contains(out, red) {
		t.Errorf("self-fan red face not coloured: %q", out)
	}
	for _, code := range []string{"\x1b[36m", "\x1b[33m", "\x1b[6m", "\x1b[3m"} { // cyan / yellow
		if strings.Contains(out, code) {
			t.Errorf("self-fan should carry no highlight colour %q: %q", code, out)
		}
	}
}

// TestPileFloatWidthInvariant checks the animated pile row is exactly w cells wide
// (colour escapes and VS15 are width-0) at every slide step, in both suit modes.
func TestPileFloatWidthInvariant(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		m := &Model{r: lipgloss.DefaultRenderer(), asciiSuits: ascii}
		m.st = newStyles(m.r)
		m.pileCur = cards("4D", "4H")
		m.pilePrev = cards("3D", "3H")
		m.pileDir = [2]int{1, 0}
		const w, h = 24, 6
		for step := 0; step <= pileSteps; step++ {
			m.pileStep = step
			for i, row := range strings.Split(m.pileFloat(w, h), "\n") {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("ascii=%v step=%d row %d width=%d want %d\n%q",
						ascii, step, i, got, w, row)
				}
			}
		}
	}
}
