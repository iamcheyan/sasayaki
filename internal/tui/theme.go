package tui

import "github.com/charmbracelet/lipgloss"

// Theme is the shared visual identity, deliberately in the same family as
// shirabe: soft violet structure on a deep charcoal background, mint for
// health, amber for attention. Color never carries meaning alone — every
// status also carries a text label or icon.
type theme struct {
	violet   lipgloss.Color
	focus    lipgloss.Color
	border   lipgloss.Color
	selected lipgloss.Color
	surface  lipgloss.Color
	mint     lipgloss.Color
	amber    lipgloss.Color
	danger   lipgloss.Color
	muted    lipgloss.Color
}

func defaultTheme() theme {
	return theme{
		// Shared musubi/shirabe dashboard tokens.  Keep these fixed instead
		// of inheriting the terminal palette so all three tools stay coherent.
		violet:   lipgloss.Color("#a78bfa"),
		focus:    lipgloss.Color("#c4b5fd"),
		border:   lipgloss.Color("#51466f"),
		selected: lipgloss.Color("#332b4d"),
		surface:  lipgloss.Color("#211d2d"),
		mint:     lipgloss.Color("#6ee7b7"),
		amber:    lipgloss.Color("#fcd34d"),
		danger:   lipgloss.Color("#fda4af"),
		muted:    lipgloss.Color("#a09daa"),
	}
}

func (t theme) base() lipgloss.Style { return lipgloss.NewStyle() }
