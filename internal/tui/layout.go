package tui

// Focus targets are ordered as [VOICE: record, VOICE: shortcut,
// RUNTIME: setup, RUNTIME: diagnose, RUNTIME: logs].
const (
	focusRecord = iota
	focusShortcut
	focusSetup
	focusDiagnose
	focusLogs
	focusCount
)

// voiceFocuses is the number of focusable rows in the VOICE card.
const voiceFocuses = 2

// cardOf returns the card index (0 = VOICE, 1 = RUNTIME) for a focus id.
func cardOf(focus int) int {
	if focus < voiceFocuses {
		return 0
	}
	return 1
}

// rowOf returns the row index within the card for a focus id.
func rowOf(focus int) int {
	if focus < voiceFocuses {
		return focus
	}
	return focus - voiceFocuses
}

// rowsIn returns the number of focusable rows in a card.
func rowsIn(card int) int {
	if card == 0 {
		return voiceFocuses
	}
	return focusCount - voiceFocuses
}

// moveFocus applies one spatial navigation step from current and returns
// the new focus id. Left/right cross cards; up/down move within the card;
// both wrap around their axis.
func moveFocus(dir string, current int) int {
	switch dir {
	case "left":
		if cardOf(current) == 1 {
			row := rowOf(current)
			if row >= voiceFocuses {
				row = voiceFocuses - 1
			}
			return focusFor(0, row)
		}
		// Wrap to the RUNTIME card.
		row := rowOf(current)
		maxRow := rowsIn(1) - 1
		if row > maxRow {
			row = maxRow
		}
		return focusFor(1, row)
	case "right":
		if cardOf(current) == 0 {
			row := rowOf(current)
			maxRow := rowsIn(1) - 1
			if row > maxRow {
				row = maxRow
			}
			return focusFor(1, row)
		}
		// Wrap to the VOICE card.
		row := rowOf(current)
		if row >= voiceFocuses {
			row = voiceFocuses - 1
		}
		return focusFor(0, row)
	case "up":
		row := rowOf(current) - 1
		if row < 0 {
			row = rowsIn(cardOf(current)) - 1
		}
		return focusFor(cardOf(current), row)
	case "down":
		row := rowOf(current) + 1
		if row >= rowsIn(cardOf(current)) {
			row = 0
		}
		return focusFor(cardOf(current), row)
	case "tab":
		return (current + 1) % focusCount
	}
	return current
}

// focusFor maps (card, row) to a focus id.
func focusFor(card, row int) int {
	if card == 0 {
		return row
	}
	return voiceFocuses + row
}

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
	maxWidth := width - 4
	if maxWidth > 124 {
		maxWidth = 124
	}
	l := layout{maxWidth: maxWidth}
	if maxWidth >= 78 {
		l.sideBySide = true
		l.cardWidth = (maxWidth - 2) / 2
	}
	if maxWidth < 40 || height < 12 {
		l.compact = true
	}
	// header + blank + cards + blank + footer
	l.totalHeight = 1 + 1 + cardHeight + 1 + 1
	return l
}

// cardHeight is the outside height of both cards, equal by construction.
// It includes the two border rows and leaves enough breathing room for the
// deliberately small main-screen content.
const cardHeight = 13
