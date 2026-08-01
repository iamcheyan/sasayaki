package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/iamcheyan/sasayaki/internal/protocol"
)

// View renders the full screen: header, cards, footer.
func (m Model) View() string {
	if m.layout.compact {
		return m.compactView()
	}
	var content string
	content += m.header() + "\n\n"
	content += m.cards() + "\n"
	content += m.footer()
	rendered := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	return m.overlayView(rendered)
}

func (m Model) header() string {
	title := "✦  sasayaki"
	subtitle := "local voice input"
	titleStyle := m.theme.base().Foreground(m.theme.violet).Bold(true)
	subStyle := m.theme.base().Foreground(m.theme.muted)
	pill := m.pill()

	line := titleStyle.Render(title) + "  " + subStyle.Render(subtitle)
	gap := m.layout.maxWidth - lipgloss.Width(line) - lipgloss.Width(pill)
	if gap < 1 {
		gap = 1
	}
	return line + strings.Repeat(" ", gap) + pill
}

// pill is the status indicator in the header: color + text label so color
// is never the only signal.
func (m Model) pill() string {
	label, color := "● READY", m.theme.mint
	dot := "●"
	switch {
	case m.state == nil:
		label, color = "○ STOPPED", m.theme.amber
	case m.state.Service != "running":
		label, color = "○ STOPPED", m.theme.amber
	case m.state.Phase == protocol.PhaseRecording:
		label, color = dot+" RECORDING", m.theme.amber
	case m.state.Phase == protocol.PhaseTranscribing:
		label, color = dot+" TRANSCRIBING", m.theme.violet
	case m.state.Phase == protocol.PhasePasting:
		label, color = dot+" PASTING", m.theme.violet
	case m.state.Phase == protocol.PhaseFailed:
		label, color = dot+" ERROR", m.theme.danger
	case !m.state.Model || !m.state.Runtime:
		label, color = "○ SETUP NEEDED", m.theme.amber
	}
	return m.theme.base().Foreground(color).Bold(true).Render(label)
}

// cards renders the VOICE / RUNTIME cards side by side or stacked.
func (m Model) cards() string {
	voice := m.voiceCard()
	runtime := m.runtimeCard()
	if m.layout.sideBySide {
		return lipgloss.JoinHorizontal(lipgloss.Top, voice, strings.Repeat(" ", 2), runtime)
	}
	return lipgloss.JoinVertical(lipgloss.Left, voice, "\n", runtime)
}

// cardFrame wraps a body in a thin rounded border with a title on the edge.
func (m Model) cardFrame(title, body string) string {
	borderColor := m.theme.muted
	if m.focusCard() == title {
		borderColor = m.theme.focus
	}
	// Lip Gloss has a lovely general border primitive, but it has no fieldset
	// legend. Build this small frame deliberately so the card title lives in
	// the top edge (the shared Shirabe visual language) instead of being a
	// redundant heading inside the card.
	width := m.layout.cardWidth
	if width < 12 {
		width = 12
	}
	inner := width - 2
	edge := m.theme.base().Foreground(borderColor)
	legend := m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" " + title + " ")
	rule := inner - lipgloss.Width(legend)
	if rule < 1 {
		rule = 1
	}
	var b strings.Builder
	b.WriteString(edge.Render("╭") + legend + edge.Render(strings.Repeat("─", rule)+"╮") + "\n")

	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	for len(lines) < cardHeight-2 {
		lines = append(lines, "")
	}
	for _, line := range lines[:cardHeight-2] {
		// Card content is intentionally written to fit the smallest full
		// layout. A final plain-space pad keeps both cards exactly aligned.
		padding := inner - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		b.WriteString(edge.Render("│") + line + strings.Repeat(" ", padding) + edge.Render("│") + "\n")
	}
	b.WriteString(edge.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// focusCard returns "VOICE" or "RUNTIME" depending on which card holds the
// current focus target.
func (m Model) focusCard() string {
	if cardOf(m.focus) == 0 {
		return "VOICE"
	}
	return "RUNTIME"
}

func (m Model) voiceCard() string {
	var b strings.Builder
	b.WriteString(m.section("RECORDING", m.focus == focusRecord))
	b.WriteString("  " + m.recordingLine() + "\n\n")
	b.WriteString(m.section("SHORTCUT", m.focus == focusShortcut))
	b.WriteString("  toggle    sasayaki toggle\n\n")
	b.WriteString(m.section("LAST", false))
	last, guidance := m.lastBlock()
	b.WriteString("  " + last + "\n")
	b.WriteString("  " + guidance)
	return m.cardFrame("VOICE", b.String())
}

func (m Model) runtimeCard() string {
	var b strings.Builder
	b.WriteString(m.section("SERVICE", false))
	b.WriteString("  " + m.serviceLine() + "\n\n")
	b.WriteString(m.section("LOCAL ENGINE", false))
	b.WriteString("  " + m.engineLine() + "\n\n")
	b.WriteString(m.section("INPUT", false))
	b.WriteString("  " + m.inputLine() + "\n\n")
	b.WriteString(m.section("ACTIONS", false))
	b.WriteString("  " + m.actionLine(focusSetup, "setup") + "\n")
	b.WriteString("  " + m.actionLine(focusDiagnose, "diagnose") + "\n")
	b.WriteString("  " + m.actionLine(focusLogs, "logs"))
	return m.cardFrame("RUNTIME", b.String())
}

// section renders the ◆ section marker, focusing the pointer when active.
func (m Model) section(name string, focused bool) string {
	pointer := "  "
	style := m.theme.base().Foreground(m.theme.muted).Bold(true)
	if focused {
		pointer = "▸ "
		style = m.theme.base().Foreground(m.theme.focus).Bold(true)
	}
	return pointer + style.Render("◆ "+name) + "\n"
}

// actionLine renders one focusable action row with a pointer marker.
func (m Model) actionLine(focus int, name string) string {
	if m.focus == focus {
		return m.theme.base().Foreground(m.theme.focus).Bold(true).Render("▸ " + name)
	}
	return "  " + name
}

func (m Model) recordingLine() string {
	if m.state == nil {
		return "○ Service stopped"
	}
	switch m.state.Phase {
	case protocol.PhaseRecording:
		return m.dot(m.theme.amber) + " Recording — press the shortcut again"
	case protocol.PhaseTranscribing:
		return m.dot(m.theme.violet) + " Transcribing…"
	case protocol.PhasePasting:
		return m.dot(m.theme.violet) + " Pasting…"
	case protocol.PhaseSucceeded:
		return m.dot(m.theme.mint) + " Ready to listen"
	case protocol.PhaseFailed:
		return m.dot(m.theme.danger) + " " + shortError(m.state.LastError)
	default:
		if !m.state.Runtime || !m.state.Model {
			return m.dot(m.theme.amber) + " Setup needed — press s"
		}
		return m.dot(m.theme.mint) + " Ready to listen"
	}
}

// lastBlock returns the LAST section text and a guidance line.
func (m Model) lastBlock() (string, string) {
	if m.state == nil {
		return "—", "Press the shortcut to record"
	}
	switch m.state.Phase {
	case protocol.PhaseSucceeded:
		if m.state.LastResult != "" {
			return "“" + truncatePlain(m.state.LastResult, 28) + "”", "Pasted into the focused window"
		}
	case protocol.PhaseFailed:
		return shortError(m.state.LastError), "See diagnose (D) for what failed"
	}
	if m.state.LastPhase == protocol.PhaseSucceeded && m.state.LastResult != "" {
		return "“" + truncatePlain(m.state.LastResult, 28) + "”", "Ready for the next clip"
	}
	return "—", "Press the shortcut to record"
}

func shortError(detail string) string {
	if detail == "" {
		return "Error"
	}
	return truncatePlain(detail, 28)
}

func truncatePlain(s string, limit int) string {
	if lipgloss.Width(s) <= limit {
		return s
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > limit-1 {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
}

func (m Model) serviceLine() string {
	if m.state == nil {
		return m.dot(m.theme.amber) + " Not running — press s to set up"
	}
	if m.state.Service == "running" {
		return m.dot(m.theme.mint) + " Active (systemd user)"
	}
	if m.state.Worker == "dead" {
		return m.dot(m.theme.danger) + " Unhealthy — press s to repair"
	}
	return m.dot(m.theme.amber) + " Stopped — press s to set up"
}

func (m Model) engineLine() string {
	if m.state == nil {
		return "○ Unknown"
	}
	model := m.mark(m.state.Model)
	runtime := m.mark(m.state.Runtime)
	return model + " model   " + runtime + " runtime"
}

func (m Model) inputLine() string {
	if m.state == nil {
		return "○ Unknown"
	}
	mic := m.mark(m.state.Microphone)
	paste := m.mark(m.state.Paste)
	backend := m.state.PasteBackend
	if backend == "" {
		backend = "none"
	}
	return mic + " mic   " + paste + " paste (" + backend + ")"
}

func (m Model) mark(ok bool) string {
	if ok {
		return m.theme.base().Foreground(m.theme.mint).Render("✓")
	}
	return m.theme.base().Foreground(m.theme.danger).Render("✗")
}

func (m Model) dot(color lipgloss.Color) string {
	return m.theme.base().Foreground(color).Render("●") + " "
}

// footer is the centered bottom line: transient notice while fresh, else
// the key hints.
func (m Model) footer() string {
	text := "t record   s setup   d disable   b shortcut   ? help   q quit"
	if m.notice != "" && time.Now().Before(m.noticeUntil) {
		text = m.notice
	}
	return lipgloss.Place(m.layout.maxWidth, 1, lipgloss.Center, lipgloss.Center,
		m.theme.base().Foreground(m.theme.muted).Render(text))
}

// compactView is the reduced view for very small terminals.
func (m Model) compactView() string {
	title := m.theme.base().Foreground(m.theme.violet).Bold(true).Render("✦ sasayaki")
	line := title + "   " + m.pill()
	hint := m.theme.base().Foreground(m.theme.muted).Render("t record · s setup · d disable · ? help · q quit")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		line+"\n"+hint)
}
