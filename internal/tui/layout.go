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
	// three-line header + blank + cards + blank + footer. Stacked cards need a second
	// card plus its separator, so a short terminal receives the compact but
	// still actionable screen instead of clipped borders.
	l.totalHeight = 3 + 1 + cardHeight + 1 + 1
	if !l.sideBySide {
		l.totalHeight += cardHeight + 1
	}
	if maxWidth < 40 || height < l.totalHeight {
		l.compact = true
	}
	return l
}

// cardHeight is the outside height of both cards, equal by construction.
// Sixteen inner rows match musubi's compact dashboard cards; the remaining
// two rows are the shared rounded frame.
const cardHeight = 18
