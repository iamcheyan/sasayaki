package tui

import (
	"path/filepath"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		StateHome:  filepath.Join(base, "state"),
		Runtime:    filepath.Join(base, "runtime"),
	}
}

func TestComputeLayoutWideSideBySide(t *testing.T) {
	l := computeLayout(120, 40)
	if !l.sideBySide {
		t.Fatal("wide terminal should render cards side by side")
	}
	if l.compact {
		t.Fatal("wide terminal must not be compact")
	}
	if l.maxWidth != 116 {
		t.Fatalf("maxWidth = %d, want 116 (capped content width)", l.maxWidth)
	}
	// Both cards must fit beside each other with a 2-col gap.
	if 2*l.cardWidth+2 > l.maxWidth {
		t.Fatalf("cards %d+%d+2 exceed maxWidth %d", l.cardWidth, l.cardWidth, l.maxWidth)
	}
}

func TestComputeLayoutNarrowStacked(t *testing.T) {
	l := computeLayout(60, 30)
	if l.sideBySide {
		t.Fatal("narrow terminal should stack the cards")
	}
	if l.compact {
		t.Fatal("60×30 is not compact")
	}
}

func TestComputeLayout80x24NoPanic(t *testing.T) {
	// The brief guarantees the TUI renders at 80×24 without panicking.
	l := computeLayout(80, 24)
	if l.compact {
		t.Fatal("80×24 should use the full view")
	}
	m := New(testPaths(t))
	m.width, m.height = 80, 24
	m.layout = computeLayout(80, 24)
	_ = m.View() // must not panic
}

func TestComputeLayoutVeryNarrowCompact(t *testing.T) {
	if l := computeLayout(30, 40); !l.compact {
		t.Fatal("very narrow should be compact")
	}
	if l := computeLayout(120, 8); !l.compact {
		t.Fatal("very short should be compact")
	}
	if l := computeLayout(4, 4); !l.compact {
		t.Fatal("tiny terminal should be compact")
	}
}

func TestComputeLayoutMaxWidthCap(t *testing.T) {
	l := computeLayout(2000, 50)
	if l.maxWidth != 124 {
		t.Fatalf("maxWidth should cap at 124, got %d", l.maxWidth)
	}
}

func TestMoveFocusHorizontal(t *testing.T) {
	cases := []struct {
		dir, from, want string
	}{
		// VOICE → RUNTIME at the same row.
		{"right", "record", "setup"},
		{"right", "shortcut", "diagnose"},
		// RUNTIME → VOICE at the same row.
		{"left", "setup", "record"},
		{"left", "diagnose", "shortcut"},
		// RUNTIME rows 3+ clamp to the last VOICE row.
		{"left", "logs", "shortcut"},
		// Right from RUNTIME wraps to VOICE (same row, clamped).
		{"right", "setup", "record"},
		{"right", "logs", "shortcut"},
	}
	for _, c := range cases {
		from := focusID(c.from)
		want := focusID(c.want)
		if got := moveFocus(c.dir, from); got != want {
			t.Fatalf("moveFocus(%s, %s) = %s, want %s", c.dir, c.from, focusName(got), c.want)
		}
	}
}

func TestMoveFocusVerticalWraps(t *testing.T) {
	// Up from the top row wraps to the bottom of the same card.
	if got := moveFocus("up", focusRecord); got != focusShortcut {
		t.Fatalf("up from record = %s", focusName(got))
	}
	if got := moveFocus("up", focusSetup); got != focusLogs {
		t.Fatalf("up from setup = %s", focusName(got))
	}
	// Down from the last row wraps to the top.
	if got := moveFocus("down", focusShortcut); got != focusRecord {
		t.Fatalf("down from shortcut = %s", focusName(got))
	}
	if got := moveFocus("down", focusLogs); got != focusSetup {
		t.Fatalf("down from logs = %s", focusName(got))
	}
}

func TestMoveFocusTabCycles(t *testing.T) {
	want := []int{focusShortcut, focusSetup, focusDiagnose, focusLogs, focusRecord}
	cur := focusRecord
	for _, w := range want {
		cur = moveFocus("tab", cur)
		if cur != w {
			t.Fatalf("tab step = %s, want %s", focusName(cur), focusName(w))
		}
	}
}

func TestCardAndRowMapping(t *testing.T) {
	if cardOf(focusRecord) != 0 || cardOf(focusShortcut) != 0 {
		t.Fatal("record/shortcut belong to VOICE")
	}
	for _, f := range []int{focusSetup, focusDiagnose, focusLogs} {
		if cardOf(f) != 1 {
			t.Fatalf("focus %s should belong to RUNTIME", focusName(f))
		}
	}
	if focusFor(0, 1) != focusShortcut || focusFor(1, 2) != focusLogs {
		t.Fatal("focusFor mapping broken")
	}
}

// --- helpers ---

func focusID(name string) int {
	switch name {
	case "record":
		return focusRecord
	case "shortcut":
		return focusShortcut
	case "setup":
		return focusSetup
	case "diagnose":
		return focusDiagnose
	case "logs":
		return focusLogs
	}
	return -1
}

func focusName(f int) string {
	switch f {
	case focusRecord:
		return "record"
	case focusShortcut:
		return "shortcut"
	case focusSetup:
		return "setup"
	case focusDiagnose:
		return "diagnose"
	case focusLogs:
		return "logs"
	}
	return "?"
}
