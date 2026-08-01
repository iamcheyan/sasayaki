package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/protocol"
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
	l := computeLayout(60, 40)
	if l.sideBySide {
		t.Fatal("narrow terminal should stack the cards")
	}
	if l.compact {
		t.Fatal("60×40 is not compact")
	}
	if l.cardWidth != l.maxWidth {
		t.Fatalf("stacked card width = %d, want full content width %d", l.cardWidth, l.maxWidth)
	}
}

func TestComputeLayout80x24NoPanic(t *testing.T) {
	// The brief guarantees the TUI renders at 80×24 without panicking.
	l := computeLayout(80, 24)
	if l.compact || !l.sideBySide {
		t.Fatal("80×24 should use the side-by-side full view")
	}
	m := New(testPaths(t))
	m.width, m.height = 80, 24
	m.layout = computeLayout(80, 24)
	_ = m.View() // must not panic
}

func TestComputeLayoutUsesCompactWhenStackedCardsCannotFit(t *testing.T) {
	l := computeLayout(60, 24)
	if !l.compact {
		t.Fatal("short stacked terminal must use compact view instead of clipping cards")
	}
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
		{"right", "model", "record"},
		{"right", "setup", "logs"},
		{"left", "record", "model"},
		{"left", "logs", "setup"},
		{"right", "diagnose", "logs"},
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
	if got := moveFocus("up", focusModel); got != focusDiagnose {
		t.Fatalf("up from model = %s", focusName(got))
	}
	if got := moveFocus("up", focusRecord); got != focusLogs {
		t.Fatalf("up from record = %s", focusName(got))
	}
	// Down from the last row wraps to the top.
	if got := moveFocus("down", focusDiagnose); got != focusModel {
		t.Fatalf("down from diagnose = %s", focusName(got))
	}
	if got := moveFocus("down", focusLogs); got != focusRecord {
		t.Fatalf("down from logs = %s", focusName(got))
	}
}

func TestMoveFocusTabCycles(t *testing.T) {
	want := []int{focusSetup, focusDiagnose, focusRecord, focusLogs, focusModel}
	cur := focusModel
	for _, w := range want {
		cur = moveFocus("tab", cur)
		if cur != w {
			t.Fatalf("tab step = %s, want %s", focusName(cur), focusName(w))
		}
	}
}

func TestCardAndRowMapping(t *testing.T) {
	if cardOf(focusModel) != 0 || cardOf(focusSetup) != 0 || cardOf(focusDiagnose) != 0 {
		t.Fatal("model/setup/diagnose belong to CONFIGURE")
	}
	for _, f := range []int{focusRecord, focusLogs} {
		if cardOf(f) != 1 {
			t.Fatalf("focus %s should belong to LIVE", focusName(f))
		}
	}
	if focusFor(0, 1) != focusSetup || focusFor(1, 1) != focusLogs {
		t.Fatal("focusFor mapping broken")
	}
}

func TestCardFrameHasBorderLegendAndFixedHeight(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	card := m.cardFrame("VOICE", "  one\n  two")
	if !strings.Contains(card, "VOICE") || !strings.Contains(card, "╭") || !strings.Contains(card, "╰") {
		t.Fatalf("card should render a titled fieldset border: %q", card)
	}
	if got := len(strings.Split(card, "\n")); got != cardHeight {
		t.Fatalf("card rows = %d, want %d", got, cardHeight)
	}
}

func TestMainScreenSeparatesConfigurationAndLiveSession(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, SpeechModel: "paraformer-zh-int8", Language: "zh"}
	view := m.View()
	for _, want := range []string{"CONFIGURE", "LIVE", "LOCAL MODEL", "Paraformer Large", "LANGUAGE & TRANSLATION", "LAST RESULT", "ACTIVITY"} {
		if !strings.Contains(view, want) {
			t.Fatalf("main screen missing %q:\n%s", want, view)
		}
	}
}

func TestTruncatePlainKeepsUTF8Valid(t *testing.T) {
	got := truncatePlain("こんにちは世界", 5)
	if got != "こん…" {
		t.Fatalf("truncatePlain = %q, want %q", got, "こん…")
	}
}

// --- helpers ---

func focusID(name string) int {
	switch name {
	case "model":
		return focusModel
	case "setup":
		return focusSetup
	case "diagnose":
		return focusDiagnose
	case "record":
		return focusRecord
	case "logs":
		return focusLogs
	}
	return -1
}

func focusName(f int) string {
	switch f {
	case focusModel:
		return "model"
	case focusSetup:
		return "setup"
	case focusDiagnose:
		return "diagnose"
	case focusRecord:
		return "record"
	case focusLogs:
		return "logs"
	}
	return "?"
}
