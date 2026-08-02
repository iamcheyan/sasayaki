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
	if !l.compact {
		t.Fatal("60×40 cannot fit two 16-row dashboard cards and should be compact")
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

func TestComputeLayoutUsesTerminalWidth(t *testing.T) {
	l := computeLayout(2000, 50)
	if l.maxWidth != 132 {
		t.Fatalf("maxWidth should cap at the shared 132-column family width, got %d", l.maxWidth)
	}
}

func TestCardFrameHasBorderLegendAndFixedHeight(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	card := m.customCardFrame("VOICE", "  one\n  two", m.layout.cardWidth, cardHeight, false)
	if !strings.Contains(card, "VOICE") || !strings.Contains(card, "╭") || !strings.Contains(card, "╰") {
		t.Fatalf("card should render a titled fieldset border: %q", card)
	}
	if got := len(strings.Split(card, "\n")); got != cardHeight {
		t.Fatalf("card rows = %d, want %d", got, cardHeight)
	}
}

func TestMainScreenMasterDetailLayout(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, SpeechModel: "sensevoice-int8", Language: "zh"}
	view := m.View()
	for _, want := range []string{"CATEGORIES", "LOCAL SPEECH MODEL", "Local Speech Model", "Speech Language", "Translation Toggle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("main screen missing %q:\n%s", want, view)
		}
	}
}
