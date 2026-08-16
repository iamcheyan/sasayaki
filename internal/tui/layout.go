package tui

// layout computes the card geometry for a terminal size. It is pure so the
// renderer and any mouse handling share the same model.
type layout struct {
	// maxWidth is the shared content width (header/cards/footer).
	maxWidth int
	// sideBySide is true when the two cards fit side by side.
	sideBySide bool
	// cardWidth is each card's width when side by side.
	cardWidth int
	// compact is true for very small terminals that render the reduced view.
	compact bool
	// cardHeight is the adaptive outside height of both cards.
	cardHeight int
	// totalHeight is the full content height including header and footer.
	totalHeight int
}

// computeLayout returns the layout for a terminal size. The geometry never
// panics at any width or height.
func computeLayout(width, height int) layout {
	if width < 8 || height < 6 {
		return layout{compact: true}
	}
	// The family uses a readable 132-column dashboard cap. The whole block is
	// centered by View(); individual rows stay left aligned inside this width.
	maxWidth := width - 4
	if maxWidth > 132 {
		maxWidth = 132
	}
	l := layout{maxWidth: maxWidth, cardWidth: maxWidth}
	// 80-column terminals have 76 usable columns after the shared margin.
	// Two 37-column cards are still legible, and this avoids turning the
	// standard 80×24 terminal into a tall, cramped stack.
	if maxWidth >= 70 {
		l.sideBySide = true
		l.cardWidth = (maxWidth - 2) / 2
	}
	// The cards grow with the settings menu (category headers + items +
	// blank separators), capped so ordinary terminals are not filled edge to
	// edge; both cards share the height so left menu and right detail stay
	// aligned.
	l.cardHeight = menuBodyHeight() + 4
	if l.cardHeight > maxCardHeight {
		l.cardHeight = maxCardHeight
	}
	if l.cardHeight < minCardHeight {
		l.cardHeight = minCardHeight
	}
	// three-line header + blank + cards + blank + footer. Stacked cards need a second
	// card plus its separator, so a short terminal receives the compact but
	// still actionable screen instead of clipped borders.
	chromeHeight := 3 + 1 + 1 + 1 // header + blank + blank + footer
	l.totalHeight = chromeHeight + l.cardHeight
	if !l.sideBySide {
		l.totalHeight += l.cardHeight + 1
	}
	// Shrink the cards (never below minCardHeight) before giving up and
	// switching to the compact view, so the historical 80×24 guarantee
	// keeps the full side-by-side dashboard. Stacked mode must fit BOTH
	// cards plus their separator inside the terminal.
	fitHeight := height - chromeHeight
	if l.sideBySide {
		if l.cardHeight > fitHeight {
			if fitHeight >= minCardHeight {
				l.cardHeight = fitHeight
				l.totalHeight = chromeHeight + l.cardHeight
			} else {
				l.compact = true
			}
		}
	} else {
		stacked := 2*l.cardHeight + 1
		if stacked > fitHeight {
			if 2*minCardHeight+1 <= fitHeight {
				l.cardHeight = (fitHeight - 1) / 2
				l.totalHeight = chromeHeight + 2*l.cardHeight + 1
			} else {
				l.compact = true
			}
		}
	}
	if maxWidth < 40 || height < l.totalHeight {
		l.compact = true
	}
	return l
}

// cardHeight is the outside height of both cards, equal by construction.
// menuBodyHeight returns the natural row count of the settings menu body:
// one row per category header, per item, plus one blank separator after each
// category (and one trailing row of breathing room).
func menuBodyHeight() int {
	rows := 0
	for _, n := range menuCategoryItems {
		rows += 1 + n + 1
	}
	return rows
}

// Layout clamps keep small terminals usable and huge ones uncrowded.
const (
	minCardHeight = 18 // matches the historical fixed height
	maxCardHeight = 26
)

const cardHeight = minCardHeight
