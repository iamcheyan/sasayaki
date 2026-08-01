package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// overlayView renders the active overlay (or the plain content when none).
// Overlays are centered boxes with a border; Esc always closes them.
func (m Model) overlayView(content string) string {
	if m.overlay == overlayNone {
		return content
	}
	var box string
	switch m.overlay {
	case overlayHelp:
		box = m.helpBox()
	case overlayKeys:
		box = m.keysBox()
	case overlayLogs:
		box = m.logsBox()
	case overlayDiag:
		box = m.diagBox()
	case overlaySetup:
		box = m.setupBox()
	case overlayConfirm:
		box = m.confirmBox()
	case overlayModels:
		box = m.modelsBox()
	}
	width := m.layout.maxWidth
	if width > 76 {
		width = 76
	}
	height := m.height - 2
	style := m.theme.base().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.focus).
		Width(width).
		Height(height)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, style.Render(box))
}

func (m Model) modelsBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Local speech model") + "\n\n")
	for i, model := range transcribe.SpeechModels {
		number := fmt.Sprintf("%d", i+1)
		installed := "not downloaded"
		if transcribe.ModelValidFor(m.paths, model.ID) {
			installed = "installed"
		}
		b.WriteString("  " + m.theme.base().Foreground(m.theme.focus).Bold(true).Render(number) + "  " + model.Label + "\n")
		b.WriteString("     " + model.Description + " [" + installed + "]\n\n")
	}
	b.WriteString("  Choosing an absent model downloads, verifies, and activates it.\n")
	b.WriteString("  Esc closes this chooser.\n")
	return b.String()
}

// boxTitle renders the overlay title line with a marker.
func (m Model) boxTitle(title string) string {
	return m.theme.base().Foreground(m.theme.violet).Bold(true).Render("◆ " + title)
}

func (m Model) helpBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Help") + "\n\n")
	rows := [][2]string{
		{"t / T", "record: start or finish voice input"},
		{"s / S", "setup: install or repair the local runtime"},
		{"d / D", "run a read-only diagnostic report"},
		{"m / M", "choose or download a local speech model"},
		{"b / B", "shortcut: how to bind the toggle"},
		{"l / L", "logs: recent service output"},
		{"?", "this help"},
		{"← ↑ → ↓ / Tab", "move between actions"},
		{"Enter", "activate the focused action"},
		{"q / Q", "quit"},
		{"Esc", "close this overlay"},
	}
	for _, row := range rows {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.focus).Bold(true).Render(row[0]))
		b.WriteString(strings.Repeat(" ", 15-lipgloss.Width(row[0])))
		b.WriteString(m.theme.base().Foreground(m.theme.muted).Render(row[1]) + "\n")
	}
	return b.String()
}

func (m Model) keysBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Global shortcut") + "\n\n")
	b.WriteString("  Bind this to a desktop keyboard shortcut:\n\n")
	b.WriteString("  " + m.theme.base().Foreground(m.theme.focus).Bold(true).Render("sasayaki toggle") + "\n\n")
	b.WriteString("  Press it once to start recording, again to finish,\n")
	b.WriteString("  transcribe and paste.\n\n")
	for _, line := range []string{
		"KDE:      System Settings → Shortcuts → Add Command",
		"GNOME:    Settings → Keyboard → Custom Shortcuts",
		"Hyprland: bind = SUPER, V, exec, sasayaki toggle",
		"Sway:     bindsym $mod+v exec sasayaki toggle",
	} {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(line) + "\n")
	}
	b.WriteString("\n  " + m.theme.base().Foreground(m.theme.muted).Render("Esc to close") + "\n")
	return b.String()
}

func (m Model) logsBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Service logs") + "\n\n")
	visible := m.logsVisible()
	start := len(m.logs) - visible - m.logScroll
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(m.logs) {
		end = len(m.logs)
	}
	lines := m.logs[start:end]
	if len(lines) == 0 {
		lines = []string{"(no log output yet)"}
	}
	for _, line := range lines {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(truncateWide(line, 68)) + "\n")
	}
	if m.logScroll > 0 || end < len(m.logs) {
		b.WriteString("\n  " + m.theme.base().Foreground(m.theme.muted).Render("↑/↓ scroll · Esc close") + "\n")
	}
	return b.String()
}

func (m Model) diagBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Diagnostics") + "\n\n")
	report := m.diag
	if !m.diagDone {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render("Running checks…") + "\n")
		return b.String()
	}
	for _, check := range report.Checks {
		mark := "✓"
		color := m.theme.mint
		if !check.OK {
			mark = "✗"
			color = m.theme.danger
		}
		b.WriteString("  " + m.theme.base().Foreground(color).Bold(true).Render(mark) + " " +
			m.theme.base().Bold(true).Render(padRight(check.Name, 16)) + " " +
			m.theme.base().Foreground(m.theme.muted).Render(truncateWide(check.Detail, 40)) + "\n")
		if !check.OK && check.Fix != "" {
			b.WriteString("    " + m.theme.base().Foreground(m.theme.amber).Render("fix: "+truncateWide(check.Fix, 54)) + "\n")
		}
	}
	for _, problem := range report.Model {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.danger).Bold(true).Render("✗") + " model: " +
			m.theme.base().Foreground(m.theme.muted).Render(truncateWide(problem, 52)) + "\n")
	}
	b.WriteString("\n  " + m.theme.base().Foreground(m.theme.muted).Render("Esc to close") + "\n")
	return b.String()
}

func (m Model) setupBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Setup") + "\n\n")
	if len(m.setupLines) == 0 {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render("Preparing…") + "\n")
	}
	// Show the most recent lines that fit.
	visible := maxInt(m.height-8, 4)
	lines := m.setupLines
	if len(lines) > visible {
		lines = lines[len(lines)-visible:]
	}
	for _, line := range lines {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(truncateWide(line, 66)) + "\n")
	}
	b.WriteString("\n  " + m.theme.base().Foreground(m.theme.muted).Render("Setup runs locally; Esc to close") + "\n")
	return b.String()
}

func (m Model) confirmBox() string {
	var b strings.Builder
	b.WriteString(m.boxTitle("Disable the service?") + "\n\n")
	b.WriteString("  This stops Sasayaki and prevents it from starting\n")
	b.WriteString("  with your user session. Your model and settings stay.\n\n")
	b.WriteString("  " + m.theme.base().Foreground(m.theme.focus).Bold(true).Render("y") + " disable   " +
		m.theme.base().Foreground(m.theme.focus).Bold(true).Render("n") + " keep running\n")
	return b.String()
}

func padRight(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

func truncateWide(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	// Trim by rune count conservatively; wide glyphs shrink further.
	cut := 0
	for range s {
		if cut >= max-1 {
			break
		}
		cut++
	}
	return s[:cut] + "…"
}
