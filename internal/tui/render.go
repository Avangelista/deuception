package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Avangelista/deuception/internal/game"
	"github.com/Avangelista/deuception/internal/protocol"
)

const (
	minW = 34
	minH = 14 // top band 2 + side fans >=5 + bottom (error 1 + hand 4 + footer 1)

	vs15 = "︎" // variation selector-15: request text (not emoji) glyph, width-1
)

// ---- player letters & labels ----

// letterFor returns a seat's chosen display letter. The A/B/C/D fallback only
// applies before the first snapshot; the letter is cosmetic, positions stay
// seat-based.
func (m *Model) letterFor(seat int) byte {
	if m.snap != nil && seat >= 0 && seat < len(m.snap.Players) {
		if l := m.snap.Players[seat].Letter; l != 0 {
			return l
		}
	}
	return byte('A' + seat)
}

func (m *Model) playerAtRel(rel int) protocol.PlayerView {
	n := len(m.snap.Players)
	return m.snap.Players[(m.snap.YouSeat+rel)%n]
}

// label renders a player as "<L> <count>". The turn cue is colour - primary on
// turn, secondary off - both the same width so the layout never drifts. The
// active-player pointer lives in the pile gap (see addTurnPointer), not here.
func (m *Model) label(p protocol.PlayerView) string {
	inner := fmt.Sprintf("%c %d", m.letterFor(p.Seat), p.CardCount)
	if m.showTurn(p) {
		return m.st.primary.Render(inner)
	}
	return m.st.secondary.Render(inner)
}

// showTurn reports whether p should be drawn as the active player: it is their turn
// and no played card is still sliding in, so the turn cue waits for the card to land.
func (m *Model) showTurn(p protocol.PlayerView) bool {
	return p.IsTurn && !m.midPlaySlide()
}

// oppMark is a player's status glyph, shown in the gap by their hand: the pointer
// (pointing at their hand) when it is their turn, ✗ if they passed, ⊘ if they left.
// pointer is the seat-specific direction (top ▴, left ◂, right ▸, self ▾).
func (m *Model) oppMark(p protocol.PlayerView, pointer string) string {
	if !p.Connected {
		return "⊘" // left the game (boss-maps back to "D")
	}
	if m.showTurn(p) {
		return pointer // their turn: a pointer at their hand (boss-maps to ^v<>)
	}
	if p.Passed {
		return "✗" // passed this trick (boss-maps back to "X")
	}
	return ""
}

// bossReplacer disguises the board as plain terminal text: it blanks the rounded
// borders and the opponent-back ░ fill to spaces (keeping columns aligned), drops
// the width-0 variation selector, and turns glyph pips back into their letters so a
// row like "│9♥ │" reads " 9H  ". bossHide first strips colour with ansi.Strip
// (SGR runs are width-0, so removing them shifts nothing), then applies the map.
var bossReplacer = strings.NewReplacer(
	"│", " ", "─", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ",
	"░", " ",
	vs15, "",
	"♦", "D", "♣", "C", "♥", "H", "♠", "S",
	// markers back to their ASCII ancestors, so boss mode stays column-identical
	"▴", "^", "▾", "v", "▸", ">", "◂", "<",
	"✗", "X", "⊘", "D", "‹", "<", "›", ">", "∙", "*",
	"|", " ", "_", " ", // legacy ASCII borders, harmless
)

func bossHide(s string) string { return bossReplacer.Replace(ansi.Strip(s)) }

// youHostTag labels a player as "(you, host)", "(you)", "(host)", or "". No
// leading space; the caller adds its own.
func youHostTag(p protocol.PlayerView) string {
	switch {
	case p.IsYou && p.IsHost:
		return "(you, host)"
	case p.IsYou:
		return "(you)"
	case p.IsHost:
		return "(host)"
	}
	return ""
}

// botTag labels a bot seat as "(lvl N bot)", or "" for a human.
func botTag(p protocol.PlayerView) string {
	if p.IsBot {
		return fmt.Sprintf("(lvl %d bot)", p.BotLevel)
	}
	return ""
}

// ---- game table (anchored to the screen edges: C top, B left, D right, A
// bottom, pile centre) ----

// tooSmall is the shared "enlarge your terminal" screen, shown once the window
// drops below the minimum.
func (m *Model) tooSmall() string {
	return m.center(fmt.Sprintf("enlarge terminal to %dx%d", minW, minH) +
		"\n" + m.st.secondary.Render(fmt.Sprintf("(now %dx%d)", m.w, m.h)))
}

func (m *Model) renderGame() string {
	n := len(m.snap.Players)
	w, h := m.w, m.h

	// Bottom edge: an always-visible error line above the hand, centred over the
	// table.
	self := lipgloss.PlaceHorizontal(w, lipgloss.Center, m.selfBand())
	footerText := gameFooter(w)
	if m.boss {
		footerText = "" // the controls legend would give the game away; blank the row
	}
	footer := lipgloss.PlaceHorizontal(w, lipgloss.Center, m.st.tertiary.Render(footerText))
	bottom := lipgloss.JoinVertical(lipgloss.Left,
		m.errorLine(w),
		self,
		footer,
	)
	bottomH := lipgloss.Height(bottom)

	// Top edge: the opponent across the table (none in 3-player).
	top := m.topBand(n, w)
	topH := 0
	if top != "" {
		topH = lipgloss.Height(top)
	}

	midH := h - topH - bottomH
	if midH < 3 {
		midH = 3
	}

	parts := make([]string, 0, 3)
	if top != "" {
		parts = append(parts, lipgloss.PlaceHorizontal(w, lipgloss.Center, top))
	}
	parts = append(parts, m.midRow(n, w, midH))
	parts = append(parts, bottom)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// topBand: the across-the-table opponent's hidden hand, anchored to the top.
func (m *Model) topBand(n, w int) string {
	var p protocol.PlayerView
	switch n {
	case 4:
		p = m.playerAtRel(2)
	case 2:
		p = m.playerAtRel(1)
	default:
		return "" // 3 players: no top seat
	}
	if p.CardCount == 0 {
		// No cards left (this player just played their last card and won): label only,
		// keeping the band's 2-row height so the board doesn't shift.
		return lipgloss.JoinVertical(lipgloss.Left, m.label(p), "")
	}
	// The label rides the floor row (same width both turns, so it never moves
	// horizontally). The band is a fixed 2 rows so the board never shifts: on turn
	// the ░ back grows down toward the centre above the floor, off turn only the
	// floor shows (receded to the top edge) and row 2 holds the last-play marker.
	active := m.showTurn(p)
	fill, floor := hFan(p.CardCount, w, active)
	// The label stays pinned to the top row. On turn the full card back shows (░ body
	// over its floor) and the ▴ turn pointer sits in the pile gap just below (see
	// pileFloat). Off turn the floor is on top and row 2 holds the status marker.
	if active {
		return lipgloss.JoinVertical(lipgloss.Left,
			m.paintBack(fill, true)+" "+m.label(p), m.paintBack(floor, true))
	}
	// Centre the marker over the whole band (label included) so it lands at screen
	// centre, aligned with the on-turn ▴ pointer (which lives in the pile gap).
	row0 := m.paintBack(floor, false) + " " + m.label(p)
	mark := lipgloss.PlaceHorizontal(lipgloss.Width(row0), lipgloss.Center, m.styleMark(m.oppMark(p, "▴")))
	return lipgloss.JoinVertical(lipgloss.Left, row0, mark)
}

// midRow: left opponent flush-left, right opponent flush-right, pile centred,
// filling exactly midH rows.
func (m *Model) midRow(n, w, midH int) string {
	if n < 3 {
		return m.pileFloat(w, midH)
	}
	left := m.playerAtRel(1)      // B
	right := m.playerAtRel(n - 1) // D in 4p, C in 3p

	sideW := w / 4
	if sideW < 8 {
		sideW = 8
	}
	centerW := w - 2*sideW
	if centerW < 8 {
		centerW = 8
		sideW = (w - centerW) / 2
	}

	leftCol := lipgloss.Place(sideW, midH, lipgloss.Left, lipgloss.Center, m.sideBlock(left, midH-1, true))
	centerCol := m.pileFloat(centerW, midH)
	rightCol := lipgloss.Place(sideW, midH, lipgloss.Right, lipgloss.Center, m.sideBlock(right, midH-1, false))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, centerCol, rightCol)
}

// pileNudge maps a relative seat to the on-screen direction of that player: self
// is bottom, and the rest are placed by seat count (see topBand/midRow). It is the
// per-play step the pile drifts toward whoever played, building a virtual stack.
func pileNudge(rel, n int) (dx, dy int) {
	if rel == 0 {
		return 0, 1 // self: bottom
	}
	switch n {
	case 2:
		return 0, -1 // the only opponent sits at the top
	case 3:
		if rel == 1 {
			return -1, 0 // left
		}
		return 1, 0 // right
	case 4:
		switch rel {
		case 1:
			return -1, 0 // left
		case 2:
			return 0, -1 // top
		default:
			return 1, 0 // right
		}
	}
	return 0, 0
}

// sideBlock: a side opponent's sideways fan, label pinned at the anchored outer
// edge (left player's big card at the bottom, right's at the top). On turn each
// card reaches toward the centre; off turn it recedes, so the label never moves.
func (m *Model) sideBlock(p protocol.PlayerView, budget int, leftSide bool) string {
	if p.CardCount == 0 {
		return m.label(p) // no cards left (this player just won): label only
	}
	var fan []string
	active := m.showTurn(p)
	align, arrow := lipgloss.Left, "◂" // left opponent: ◂ points left at their fan
	if leftSide {
		fan = vFanLeft(p.CardCount, budget, active)
	} else {
		fan = vFanRight(p.CardCount, budget, active)
		align, arrow = lipgloss.Right, "▸" // right opponent: ▸ points right at their fan
	}
	// Paint the fan first: primary outline on their turn (blue ░), else secondary gray.
	for i := range fan {
		fan[i] = m.paintBack(fan[i], active)
	}
	// Then inject the (secondary) status marker on the centre-facing side, vertically
	// centred: the pointer at their fan on turn, ✗ passed, ⊘ gone. Injected after the
	// paint so it stays secondary even when the active border is primary.
	if mark := m.styleMark(m.oppMark(p, arrow)); mark != "" && len(fan) > 0 {
		mid := len(fan) / 2
		if leftSide {
			fan[mid] = fan[mid] + " " + mark
		} else {
			fan[mid] = mark + " " + fan[mid]
		}
	}
	// The label sits on its own row, so the side fan gets no gap before it.
	return lipgloss.JoinVertical(align, append(fan, m.label(p))...)
}

// pileBoxLines renders one played combo as the 4 rows of a horizontal face-up box.
// Each card is its own rounded tile; overlapping cards keep their own ╭/╰ corners
// (a "rounded background"), and the front card widens by two to match the hand:
//
//	╭──╭──╭────╮
//	│4♦│4♥│2♠  │
//	│  │  │    │
//	╰──╰──╰────╯
//
// Widths match the old ASCII box exactly (non-front 3 cells, front 6), so the pile
// centring and slide math are untouched. Suit colour/text-presentation is applied
// later by paintPileRow; these lines are bare width-1 runes.
func (m *Model) pileBoxLines(cs []game.Card) []string {
	if len(cs) == 0 {
		return nil
	}
	var top, face, body, bottom strings.Builder
	for i, c := range cs {
		face.WriteRune('│')
		face.WriteRune(c.Rank.Rune())
		face.WriteRune(m.suitRune(c.Suit))
		if i == len(cs)-1 { // wider "big" front card
			top.WriteString("╭────╮")
			face.WriteString("  │")
			body.WriteString("│    │")
			bottom.WriteString("╰────╯")
		} else {
			top.WriteString("╭──")
			body.WriteString("│  ")
			bottom.WriteString("╰──")
		}
	}
	return []string{top.String(), face.String(), body.String(), bottom.String()}
}

// paintPileRow renders a composited pile row. The pile is the "card in the middle",
// so its borders stay primary; a red card's face (rank + pip) goes red, black faces
// stay primary too. Colouring is a pure function of the row (suits never collide
// with ranks/borders), so the pile needs no separate tag grid - it builds one here
// and reuses paintTagged.
func (m *Model) paintPileRow(row []rune) string {
	tags := make([]uint8, len(row))
	for i, r := range row {
		switch r {
		case '▴', '▾', '◂', '▸': // the active-turn pointer in the pile gap
			tags[i] = tagSecondary
			continue
		}
		if isRed, isSuit := m.suitInfo(r); isSuit && isRed {
			tags[i] = tagRed
			if i > 0 && tags[i-1] == tagPlain { // the rank sits just left of its pip
				tags[i-1] = tagRed
			}
		}
	}
	return m.paintTagged(row, tags)
}

// pileFloat draws the pile in a w x h block. The current play rests centred; when a
// new play arrives it slides in from the side of the player who made it, starting
// fully off the block edge so it enters clipped (top/bottom or side cut off) and
// glides fully into view - a real entrance even when there is little room to travel.
// It opaquely covers the play it beat; within a trick every play is the same size,
// so at rest the incoming card covers the previous one exactly - no visible stack.
func (m *Model) pileFloat(w, h int) string {
	grid := make([][]rune, h)
	for r := range grid {
		grid[r] = []rune(strings.Repeat(" ", w))
	}
	// The play being covered sits centred underneath the incoming card.
	if prev := m.pileBoxLines(m.pilePrev); len(prev) > 0 {
		pasteBox(grid, prev, (w-boxWidth(prev))/2, (h-len(prev))/2)
	}
	// The current play glides from its side (step 0) to centre (step pileSteps).
	if box := m.pileBoxLines(m.pileCur); len(box) > 0 {
		bw, bh := boxWidth(box), len(box)
		endX, endY := (w-bw)/2, (h-bh)/2
		startX, startY := endX, endY
		switch {
		case m.pileDir[0] > 0:
			startX = w // fully off the right edge: enters clipped, slides left
		case m.pileDir[0] < 0:
			startX = -bw // fully off the left edge
		}
		switch {
		case m.pileDir[1] > 0:
			startY = h // fully below: enters clipped, slides up
		case m.pileDir[1] < 0:
			startY = -bh // fully above
		}
		step := clampi(m.pileStep, 0, pileSteps)
		x := startX + (endX-startX)*step/pileSteps
		y := startY + (endY-startY)*step/pileSteps
		pasteBox(grid, box, x, y)
	}
	// Active top opponent: a ▴ pointer in the gap just above the pile, pointing up at
	// their hand (the top band is full on turn, so its pointer lives here; the self ▾
	// rides the error line and the sides use their fan). Boss-maps to ^.
	if m.snap != nil && h > 0 && w > 0 {
		var top protocol.PlayerView
		ok := false
		switch len(m.snap.Players) {
		case 4:
			top, ok = m.playerAtRel(2), true
		case 2:
			top, ok = m.playerAtRel(1), true
		}
		if ok && m.showTurn(top) {
			grid[0][w/2] = '▴'
		}
	}
	out := make([]string, h)
	for r := range grid {
		out[r] = m.paintPileRow(grid[r])
	}
	return strings.Join(out, "\n")
}

// boxWidth is the widest line in a rendered card box, in display cells. Every glyph
// in a box is width-1, so the rune count is the display width.
func boxWidth(box []string) int {
	w := 0
	for _, l := range box {
		if n := len([]rune(l)); n > w {
			w = n
		}
	}
	return w
}

// pasteBox draws box opaquely at (x0,y0) onto grid, clipped to the grid. Every cell
// is written, including the card's blank body, so it hides whatever is behind it - a
// card in front, not a stack. The grid is one rune per display cell, so multi-byte
// pips are copied and clipped whole (never split mid-rune).
func pasteBox(grid [][]rune, box []string, x0, y0 int) {
	h := len(grid)
	w := 0
	if h > 0 {
		w = len(grid[0])
	}
	for r, line := range box {
		gy := y0 + r
		if gy < 0 || gy >= h {
			continue
		}
		for c, ch := range []rune(line) {
			if gx := x0 + c; gx >= 0 && gx < w {
				grid[gy][gx] = ch
			}
		}
	}
}

// selfBand: the viewer's hand as a fanned row. A selected card lifts one row so
// its whole box clears the divider; an unselected one sits low, its bottom border
// off past the divider. The cursor card carries a "*" on its body. The rightmost
// is the "big" card; the label sits on the bottom row, and "<"/">" flag a scrolled
// window.
func (m *Model) selfBand() string {
	me := m.snap.Players[m.snap.YouSeat]
	hand := m.hand()
	label := m.label(me)
	// Emptied hand (you played your last card and won): no cards, just the label
	// pinned at the bottom row so the band keeps its height.
	if len(hand) == 0 {
		return "\n\n\n  " + label
	}
	myTurn := m.isMyTurn()
	maxCells := m.maxHandCells()

	var start, end int
	var moreLeft, moreRight bool
	if myTurn {
		// cursor-centred window that keeps the cursor in view
		start, end, moreLeft, moreRight = windowIndices(len(hand), m.cursor, maxCells)
	} else {
		// off turn: no cursor, scroll straight from m.scroll (the leftmost visible
		// card) so you can still look through the whole hand
		start = clampi(m.scroll, 0, maxi(0, len(hand)-maxCells))
		end = start + maxCells
		if end > len(hand) {
			end = len(hand)
		}
		moreLeft, moreRight = start > 0, end < len(hand)
	}

	runeRows, tagRows := m.selfFan(hand, start, end, m.cursor, myTurn)
	if !myTurn {
		// Off turn the hand drops a row and sheds its cursor row; selfFan puts the
		// top border at [1] and faces at [2], with the last-play marker ("^" played,
		// "X" passed) riding just above.
		totalW := len(runeRows[0])
		mrow, mtag := make([]rune, totalW), make([]uint8, totalW)
		for i := range mrow {
			mrow[i] = ' '
		}
		// Your status marker (▾ turn / ✗ passed) lives in the error line above the
		// hand, screen-centred, so this row stays blank.
		runeRows = [][]rune{{}, mrow, runeRows[1], runeRows[2]}
		tagRows = [][]uint8{{}, mtag, tagRows[1], tagRows[2]}
	}
	// 2-col left margin keeps the fan aligned. The ‹/› scroll flags (tertiary) ride on
	// row 2 either way (the face row on turn, a row above the dropped cards off turn),
	// overwriting the margin. All edits stay in the rune/tag domain so painting can
	// come last (a byte-slice of a coloured row would corrupt the escapes).
	for r := range runeRows {
		runeRows[r] = append([]rune{' ', ' '}, runeRows[r]...)
		tagRows[r] = append([]uint8{tagPlain, tagPlain}, tagRows[r]...)
	}
	if moreLeft {
		runeRows[2][0], runeRows[2][1] = '‹', ' '
		tagRows[2][0] = tagTertiary
	}
	if moreRight {
		runeRows[2] = append(runeRows[2], ' ', '›')
		tagRows[2] = append(tagRows[2], tagPlain, tagTertiary)
	}
	painted := make([]string, len(runeRows))
	for r := range runeRows {
		painted[r] = m.paintTagged(runeRows[r], tagRows[r])
	}
	painted[3] += " " + label // a gap between the hand and the label
	return lipgloss.JoinVertical(lipgloss.Left, painted...)
}

// maxHandCells is how many hand cards fit across the screen, reserving the 2-col
// left margin, box overhead and trailing label.
func (m *Model) maxHandCells() int {
	me := m.snap.Players[m.snap.YouSeat]
	n := (m.w - 6 - lipgloss.Width(m.label(me))) / 3
	if n < 1 {
		n = 1
	}
	return n
}

// colour tags for a composited fan cell: a red card's face (tagRed), and the gray
// text-hierarchy tiers used by the cursor / scroll flags / self last-play marker.
const (
	tagPlain uint8 = iota
	tagRed
	tagSecondary
	tagTertiary
)

// selfFan renders the windowed hand as a fixed 4-row fan of rounded tiles. Each card
// is its own ╭─╮│╰╯ box, so overlaps keep a rounded background; an unselected card
// sits low (its bottom ╰── off-grid past the divider), a selected card lifts to row 0
// so its whole box (and bottom) shows and pops above the row. The cursor card carries
// a "*" on its body row. Returns the rune grid and a parallel colour-tag grid;
// painting is deferred so selfBand can still edit rows structurally.
func (m *Model) selfFan(hand []game.Card, start, end, cursor int, showCursor bool) ([][]rune, [][]uint8) {
	count := end - start
	totalW := 3*(count-1) + 6 // last card sits at 3*(count-1), front cell is 6 wide
	rows := make([][]rune, 4)
	tags := make([][]uint8, 4)
	for r := range rows {
		rows[r] = []rune(strings.Repeat(" ", totalW))
		tags[r] = make([]uint8, totalW)
	}
	put := func(r, c int, g rune, tag uint8) {
		if r >= 0 && r < 4 && c >= 0 && c < totalW {
			rows[r][c] = g
			tags[r][c] = tag
		}
	}
	// Borders read primary on your turn (the active player), secondary otherwise.
	borderTag := uint8(tagSecondary)
	if showCursor {
		borderTag = tagPlain
	}
	for j := 0; j < count; j++ {
		i := start + j
		L := 3 * j
		faceW := 2
		front := j == count-1
		if front {
			faceW = 4 // the front "big" card
		}
		t := 1
		if m.selected[i] {
			t = 0 // selected: lifted up one row
		}
		faceRow, bodyRow, botRow := t+1, t+2, t+3
		// The border reads primary on your turn else secondary (borderTag); the
		// cursor is primary. Selection is the lift - geometry, not colour.
		// Roof ╭──, opened to ╭────╮ when this lifted card pops a row above a lower
		// next card; left/bottom │…╰── (the bottom is on-grid only when lifted).
		open := false
		if !front {
			nextT := 1
			if m.selected[start+j+1] {
				nextT = 0
			}
			open = t < nextT
		}
		roofEnd := L + faceW
		if open {
			roofEnd = L + 4
		}
		put(t, L, '╭', borderTag)
		for c := L + 1; c <= roofEnd; c++ {
			put(t, c, '─', borderTag)
		}
		if front || open {
			put(t, roofEnd+1, '╮', borderTag)
		}
		put(faceRow, L, '│', borderTag)
		put(bodyRow, L, '│', borderTag)
		put(botRow, L, '╰', borderTag)
		for c := L + 1; c <= L+faceW; c++ {
			put(botRow, c, '─', borderTag)
		}
		if front { // the "big" card closes its own right edge
			rb := L + faceW + 1
			put(faceRow, rb, '│', borderTag)
			put(bodyRow, rb, '│', borderTag)
			put(botRow, rb, '╯', borderTag)
		}
		// Face rank+suit, coloured together (red for hearts/diamonds).
		face := hand[i]
		faceTag := tagPlain
		if face.Suit.IsRed() {
			faceTag = tagRed
		}
		put(faceRow, L+1, face.Rank.Rune(), faceTag)
		put(faceRow, L+2, m.suitRune(face.Suit), faceTag)
		if showCursor && i == cursor {
			put(bodyRow, L+1, '∙', tagPlain) // picker: primary (boss-maps to *)
		}
	}
	return rows, tags
}

// paintTagged renders a fan row from its runes and colour tags: red-tagged cells
// (a red card's rank+suit) go red, pips get text presentation (VS15). Adjacent
// same-tag cells are grouped so each colour run is one escape.
func (m *Model) paintTagged(runes []rune, tags []uint8) string {
	var b strings.Builder
	for i := 0; i < len(runes); {
		t := tags[i]
		j := i
		for j < len(runes) && tags[j] == t {
			j++
		}
		var seg strings.Builder
		for _, r := range runes[i:j] {
			seg.WriteRune(r)
			if !m.asciiSuits {
				if _, isSuit := m.suitInfo(r); isSuit {
					seg.WriteString(vs15)
				}
			}
		}
		s := seg.String()
		switch t {
		case tagRed:
			s = m.st.suitRed.Render(s)
		case tagSecondary:
			s = m.st.secondary.Render(s)
		case tagTertiary:
			s = m.st.tertiary.Render(s)
		}
		b.WriteString(s)
		i = j
	}
	return b.String()
}

// markTier returns the colour tag for a status marker: all of them (the turn
// pointer, passed ✗, gone ⊘) read secondary.
func markTier(mark string) uint8 {
	if mark == "" {
		return tagPlain
	}
	return tagSecondary
}

// styleMark colours a marker string secondary (for the string call sites; the
// rune-grid self marker uses markTier + paintTagged instead).
func (m *Model) styleMark(mark string) string {
	if mark == "" {
		return mark
	}
	return m.st.secondary.Render(mark)
}

// paintBack colours an opponent card-back row: the ░ pattern goes blue; the outline
// (and any injected marker glyph) is primary when it is that player's turn, else
// secondary gray; spaces stay bare. Runs are grouped by kind (fill / blank / other)
// so each is a single escape.
func (m *Model) paintBack(s string, active bool) string {
	outline := m.st.secondary
	if active {
		outline = m.st.primary // active player's card borders read primary
	}
	var b strings.Builder
	runes := []rune(s)
	kind := func(r rune) int {
		switch r {
		case '░':
			return 1
		case ' ':
			return 2
		}
		return 0 // outline / marker
	}
	for i := 0; i < len(runes); {
		k := kind(runes[i])
		j := i
		for j < len(runes) && kind(runes[j]) == k {
			j++
		}
		seg := string(runes[i:j])
		switch k {
		case 1:
			seg = m.st.back.Render(seg) // ░ pattern: blue
		case 0:
			seg = outline.Render(seg)
		}
		b.WriteString(seg)
		i = j
	}
	return b.String()
}

// errorLine is the always-visible line above the hand: an inline error/hint when
// there is one, else your own status marker centred over the board - ▾ pointing down
// at your hand on your turn, ✗ if you passed - else blank. Both the turn pointer and
// the passed mark share this one screen-centred slot so they never drift apart.
func (m *Model) errorLine(w int) string {
	if m.hint != "" {
		return m.r.NewStyle().Width(w).Align(lipgloss.Center).Render(m.hint)
	}
	if m.snap != nil && m.snap.YouSeat < len(m.snap.Players) {
		if mk := m.oppMark(m.snap.Players[m.snap.YouSeat], "▾"); mk != "" {
			return lipgloss.PlaceHorizontal(w, lipgloss.Center, m.styleMark(mk))
		}
	}
	return ""
}

// ---- card-back fans (front card drawn larger, like a real fan) ----

// hFan draws the top opponent's fan of rounded card-backs as two rows: a ░-filled
// body (fill, shown only on their turn) over a rounded floor. The wide front card
// is leftmost; slivers fan out to the right, each keeping its own rounded ╯ corner.
// Capped to what fits the width (minimum 3 cards).
func hFan(count, w int, active bool) (fill, floor string) {
	cap := (w - 12) / 3
	if cap < 3 {
		cap = 3
	}
	n := count
	if n > cap {
		n = cap
	}
	if n < 1 {
		n = 1
	}
	var fb, fl strings.Builder
	fb.WriteString("│ ░░ │") // wide front card, spaced ░ checker
	fl.WriteString("╰────╯")
	for i := 1; i < n; i++ {
		fb.WriteString("░ │") // sliver
		fl.WriteString("──╯")
	}
	if !active {
		return "", fl.String()
	}
	return fb.String(), fl.String()
}

// vFanLeft draws the left opponent's sideways fan, larger front card at the
// bottom, slivers showing the centre-facing right edge. active widens each card's
// body toward the centre; off turn it shrinks so the card recedes to the anchored
// left edge.
func vFanLeft(count, budget int, active bool) []string {
	// Each card shows its centre-facing (right) edge: a ╮ top corner then a │ border
	// (2-row sliver), the wide front card (4 rows, ╮ │ │ ╯) at the bottom. On their
	// turn the body opens toward the centre with ──/░ (only the ░ is blue).
	top, border, bot := "╮", "│", "╯"
	if active {
		top, border, bot = "──╮", "░ │", "──╯"
	}
	slivers := vFanSlivers(count, budget)
	rows := make([]string, 0, 2*slivers+4)
	for i := 0; i < slivers; i++ {
		rows = append(rows, top, border)
	}
	return append(rows, top, border, border, bot) // wide front card, at the bottom
}

// vFanSlivers is how many 2-row sliver backs fit above/below the 4-row front card.
func vFanSlivers(count, budget int) int {
	n := (budget - 4) / 2
	if n < 0 {
		n = 0
	}
	if n > count-1 {
		n = count - 1
	}
	return n
}

// vFanRight mirrors vFanLeft for the right opponent: larger front card at the top,
// slivers showing the centre-facing left edge, receding to the anchored right edge
// off turn.
func vFanRight(count, budget int, active bool) []string {
	// Mirror of vFanLeft: centre-facing edge on the left (╭ │ ╰), body opening to the
	// right. The wide front card (4 rows, ╭ │ │ ╰) is at the TOP; slivers (│ then the
	// ╰ bottom corner) hang below it.
	top, border, bot := "╭", "│", "╰"
	if active {
		top, border, bot = "╭──", "│ ░", "╰──"
	}
	slivers := vFanSlivers(count, budget)
	rows := make([]string, 0, 2*slivers+4)
	rows = append(rows, top, border, border, bot) // wide front card, at the top
	for i := 0; i < slivers; i++ {
		rows = append(rows, border, bot)
	}
	return rows
}

// ---- waiting room ----

func (m *Model) renderWaiting() string {
	s := m.snap
	var b strings.Builder
	for i := 0; i < s.MaxSeats; i++ {
		if i >= len(s.Players) {
			b.WriteString(m.st.tertiary.Render("-") + "\n") // empty seat recedes
			continue
		}
		p := s.Players[i]
		line := string(m.letterFor(p.Seat)) // the identity: primary (default fg)
		switch {
		case botTag(p) != "":
			line += m.st.secondary.Render(" " + botTag(p))
		case youHostTag(p) != "":
			line += m.st.secondary.Render(" " + youHostTag(p))
		}
		if !p.Connected {
			line += m.st.tertiary.Render(" (gone)")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	// Status first, so the host always sees how to start (or why they can't yet).
	// Actionable status is primary; a blocked/passive one recedes to secondary.
	switch {
	case s.IsHost && len(s.Players) >= s.MinStart:
		b.WriteString(m.st.primary.Render("enter  start") + "\n")
	case s.IsHost:
		b.WriteString(m.st.secondary.Render(fmt.Sprintf("need %d+ to start", s.MinStart)) + "\n")
	default:
		b.WriteString(m.st.secondary.Render("waiting for host...") + "\n")
	}
	legend := []string{"a-z    pick letter"}
	if s.IsHost {
		legend = append(legend, fmt.Sprintf("1-9    bot level (%d)", m.pendingBotLevel), "+/-    add/remove bot")
	}
	legend = append(legend, "esc    quit")
	b.WriteString("\n" + m.st.secondary.Render(strings.Join(legend, "\n")))
	if m.joinHint != "" {
		b.WriteString("\n\n" + m.st.secondary.Render(m.joinHint))
	}
	return m.centerBlock(b.String())
}

// ---- game over ----

func (m *Model) renderOver() string {
	s := m.snap
	var b strings.Builder
	if s.Winner >= 0 {
		b.WriteString(m.st.primary.Render(fmt.Sprintf("%c wins", m.letterFor(s.Winner))) + "\n\n")
	}
	// Scoreboard rows read like the lobby roster: primary letter+score, secondary
	// tags (one space before the tag, as in the lobby). The winner is named by the
	// headline, not by a per-row colour.
	for _, p := range rankByScore(s.Players) {
		tag := ""
		switch {
		case botTag(p) != "":
			tag = " " + botTag(p)
		case youHostTag(p) != "":
			tag = " " + youHostTag(p)
		}
		row := m.st.primary.Render(fmt.Sprintf("%c %4d", m.letterFor(p.Seat), p.Score))
		if tag != "" {
			row += m.st.secondary.Render(tag)
		}
		b.WriteString(row + "\n")
	}
	b.WriteString("\n")
	if s.IsHost {
		b.WriteString(m.st.primary.Render("enter  next hand"))
	} else {
		b.WriteString(m.st.secondary.Render("waiting for host..."))
	}
	// Blank line before the esc legend, matching the lobby's status/legend spacing.
	b.WriteString("\n\n" + m.st.secondary.Render("esc    quit"))
	return m.centerBlock(b.String())
}

// centerBlock left-aligns a multi-line block into a rectangle and centres it on
// screen.
func (m *Model) centerBlock(content string) string {
	content = strings.TrimRight(content, "\n")
	block := m.r.NewStyle().Width(lipgloss.Width(content)).Align(lipgloss.Left).Render(content)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, block)
}

func rankByScore(players []protocol.PlayerView) []protocol.PlayerView {
	out := append([]protocol.PlayerView(nil), players...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score < out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---- kicked ----

func (m *Model) renderKicked() string {
	return m.centerBlock(m.kicked + "\n\n" + m.st.tertiary.Render("press any key to disconnect"))
}

// gameFooter is the always-present in-game key legend, shortened on narrow
// terminals so it never wraps past the board.
func gameFooter(w int) string {
	full := "arrows move  space pick  enter play  x pass  c clear  s sort  esc quit"
	if lipgloss.Width(full) <= w {
		return full
	}
	return "arrows  space  enter  x  c  s  esc"
}

// ---- helpers ----

func windowIndices(n, cursor, maxCells int) (start, end int, left, right bool) {
	if n <= maxCells {
		return 0, n, false, false
	}
	start = cursor - maxCells/2
	if start < 0 {
		start = 0
	}
	if start+maxCells > n {
		start = n - maxCells
	}
	end = start + maxCells
	return start, end, start > 0, end < n
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
