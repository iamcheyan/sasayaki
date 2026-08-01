package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Theme is the shared visual identity, deliberately in the same family as
// shirabe: soft violet structure on a deep charcoal background, mint for
// health, amber for attention. Color never carries meaning alone — every
// status also carries a text label or icon.
type theme struct {
	violet  lipgloss.Color
	focus   lipgloss.Color
	mint    lipgloss.Color
	amber   lipgloss.Color
	danger  lipgloss.Color
	muted   lipgloss.Color
	bg      lipgloss.Color
	surface lipgloss.Color
}

func defaultTheme() theme {
	return theme{
		violet:  lipgloss.Color("#a78bfa"),
		focus:   lipgloss.Color("#c4b5fd"),
		mint:    lipgloss.Color("#6ee7b7"),
		amber:   lipgloss.Color("#fcd34d"),
		danger:  lipgloss.Color("#fda4af"),
		muted:   lipgloss.Color("#8b84a0"),
		bg:      lipgloss.Color("#17151f"),
		surface: lipgloss.Color("#211d2d"),
	}
}

// noColor reports whether the terminal wants plain output. NO_COLOR and
// TERM=dumb are honored; the rendering falls back to text labels only.
func noColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return true
	}
	return os.Getenv("TERM") == "dumb" || os.Getenv("TERM") == ""
}

func (t theme) base() lipgloss.Style { return lipgloss.NewStyle() }
