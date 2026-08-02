package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// overlayView renders the active overlay on top of the main background screen.
// Overlays float above the main interface so the background remains visible.
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
	case overlayTestSpeech:
		box = m.testSpeechBox()
	case overlayTestTranslation:
		box = m.testTranslationBox()
	}
	width := m.layout.maxWidth
	if width > 76 {
		width = 76
	}
	if m.overlay == overlayTestSpeech || m.overlay == overlayTestTranslation {
		// The speech/translation test shows long transcripts and live logs;
		// give it the full terminal width and as much height as fits.
		width = m.layout.maxWidth
		if width > 100 {
			width = 100
		}
	}
	style := m.theme.base().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.focus).
		Width(width)

	if m.overlay == overlayLogs || m.overlay == overlayTestSpeech || m.overlay == overlayTestTranslation {
		style = style.Height(m.height - 2)
	} else {
		maxH := m.height - 4
		if maxH < 6 {
			maxH = 6
		}
		style = style.MaxHeight(maxH)
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, style.Render(box))
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
		{"r / R", "restart: restart systemd background service"},
		{"s / S", "setup: install or repair the local runtime"},
		{"d / D", "run a read-only diagnostic report"},
		{"m / M", "choose or download a local speech model"},
		{"g / G", "language: cycle speech recognition language"},
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
		for _, wrapped := range wrapWide(line, 68) {
			b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(wrapped) + "\n")
		}
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
			m.theme.base().Foreground(m.theme.muted).Render(check.Detail) + "\n")
		if !check.OK && check.Fix != "" {
			b.WriteString("    " + m.theme.base().Foreground(m.theme.amber).Render("fix: "+check.Fix) + "\n")
		}
	}
	for _, problem := range report.Model {
		b.WriteString("  " + m.theme.base().Foreground(m.theme.danger).Bold(true).Render("✗") + " model: " +
			m.theme.base().Foreground(m.theme.muted).Render(problem) + "\n")
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
	visible := max(m.height-8, 4)
	lines := m.setupLines
	if len(lines) > visible {
		lines = lines[len(lines)-visible:]
	}
	for _, line := range lines {
		for _, wrapped := range wrapWide(line, 66) {
			b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(wrapped) + "\n")
		}
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

// testPaneGeom captures the rendered geometry of the two test-overlay panes
// for the current model state: heights, max scroll offsets, and the effective
// offsets (follow mode pins the logs offset to the newest line).
//
// View() runs on a value receiver, so renders cannot persist scroll state
// back into the Model; both the render path and the key handler compute the
// same geometry from here instead.
type testPaneGeom struct {
	resultH, logsH           int
	resultMax, logsMax       int
	resultOffset, logsOffset int
}

func (m Model) testPaneGeom(resultBody, logsBody []string) testPaneGeom {
	innerH := m.height - 4 // box height minus the two border rows
	if innerH < 6 {
		innerH = 6
	}
	paneBudget := innerH - 4 // rows for both panes plus the divider
	maxResult := paneBudget - 4
	if maxResult < 1 {
		maxResult = 1
	}
	resultH := len(resultBody)
	if resultH > maxResult {
		resultH = maxResult
	}
	if resultH < 1 {
		resultH = 1
	}
	logsH := paneBudget - resultH
	if logsH < 1 {
		logsH = 1
	}

	resultMax := len(resultBody) - resultH
	if resultMax < 0 {
		resultMax = 0
	}
	logsMax := len(logsBody) - logsH
	if logsMax < 0 {
		logsMax = 0
	}

	resultOffset := m.testResultScroll
	if resultOffset > resultMax {
		resultOffset = resultMax
	}
	if resultOffset < 0 {
		resultOffset = 0
	}
	logsOffset := m.testLogScroll
	if m.testFollow {
		logsOffset = logsMax
	}
	if logsOffset > logsMax {
		logsOffset = logsMax
	}
	if logsOffset < 0 {
		logsOffset = 0
	}
	return testPaneGeom{resultH, logsH, resultMax, logsMax, resultOffset, logsOffset}
}

// testPaneBodies builds the rendered content of both panes for the active
// test overlay.
func (m Model) testPaneBodies() (resultBody, logsBody []string) {
	wrapW := m.testPopupWidth() - 7 // indent 4 within the scrollbar gutter
	switch m.overlay {
	case overlayTestSpeech:
		return m.testSpeechResultBody(wrapW), m.testLogsBody(wrapW)
	case overlayTestTranslation:
		return m.testTranslationResultBody(wrapW), m.testLogsBody(wrapW)
	}
	return nil, nil
}

// testBox renders a test overlay split into two independently scrollable
// panes: the recognition result on top (auto-sized to its content, capped so
// the logs always keep some room) and the live service logs below. Each pane
// has its own scrollbar; the active pane's bar is highlighted. The logs pane
// auto-follows the newest line unless the user scrolled away (testFollow).
func (m Model) testBox(title string, resultBody, logsBody []string, footer string) string {
	g := m.testPaneGeom(resultBody, logsBody)

	innerW := m.testPopupWidth() - 2 // minus the two border columns
	lineW := innerW - 1              // reserve the scrollbar column

	var b strings.Builder
	b.WriteString(m.boxTitle(title) + "\n\n")

	resultBar := []rune(scrollbar(len(resultBody), g.resultH, g.resultOffset))
	for i := 0; i < g.resultH; i++ {
		line := ""
		if idx := g.resultOffset + i; idx < len(resultBody) {
			line = padRight(resultBody[idx], lineW)
		} else {
			line = strings.Repeat(" ", lineW)
		}
		if len(resultBar) > 0 {
			line += m.scrollbarGlyph(resultBar[i], m.testActivePane == testPaneResult)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString(m.theme.base().Foreground(m.theme.muted).Render(strings.Repeat("─", innerW)) + "\n")

	logsBar := []rune(scrollbar(len(logsBody), g.logsH, g.logsOffset))
	for i := 0; i < g.logsH; i++ {
		line := ""
		if idx := g.logsOffset + i; idx < len(logsBody) {
			line = padRight(logsBody[idx], lineW)
		} else {
			line = strings.Repeat(" ", lineW)
		}
		if len(logsBar) > 0 {
			line += m.scrollbarGlyph(logsBar[i], m.testActivePane == testPaneLogs)
		}
		b.WriteString(line + "\n")
	}

	if g.resultMax > 0 || g.logsMax > 0 {
		footer += "  ·  " + m.theme.base().Foreground(m.theme.muted).Render("↑/↓ scroll")
	}
	if g.resultMax > 0 {
		footer += "  ·  " + m.theme.base().Foreground(m.theme.muted).Render("Tab switch")
	}
	if g.logsMax > 0 && !m.testFollow {
		footer += "  ·  " + m.theme.base().Foreground(m.theme.muted).Render("↓ follow")
	}
	b.WriteString(padRight(footer, innerW) + "\n")
	return b.String()
}

// scrollbarGlyph styles one scrollbar column glyph, highlighting the bar of
// the pane the scroll keys currently act on.
func (m Model) scrollbarGlyph(r rune, active bool) string {
	color := m.theme.muted
	if active {
		color = m.theme.focus
	}
	return m.theme.base().Foreground(color).Render(string(r))
}

// testPopupWidth is the content width of the enlarged test overlay.
func (m Model) testPopupWidth() int {
	w := m.layout.maxWidth
	if w > 100 {
		w = 100
	}
	if w < 40 {
		w = 40
	}
	return w
}

func (m Model) testSpeechBox() string {
	wrapW := m.testPopupWidth() - 7 // indent 4 within the scrollbar gutter

	resultBody := m.testSpeechResultBody(wrapW)
	logsBody := m.testLogsBody(wrapW)

	footer := "  " + m.theme.base().Foreground(m.theme.focus).Render("Space/Enter/t") + " start/stop  ·  " +
		m.theme.base().Foreground(m.theme.focus).Render("Esc") + " cancel/exit"
	return m.testBox("Local Speech Recognition Test", resultBody, logsBody, footer)
}

// modelStatusLine renders the model/runtime readiness row shown at the top
// of the test overlay. It tells the user at a glance whether the ONNX model
// is loaded in RAM (the engine worker is warm) — the "how it works" cue the
// reference UI surfaces as "Daemon Status: Active (RAM Loaded)".
func (m Model) modelStatusLine() []string {
	label := "Model: "
	if m.state != nil && m.state.SpeechModel != "" {
		if sm, found := transcribe.SpeechModelByID(m.state.SpeechModel); found {
			label += sm.Label
		} else {
			label += m.state.SpeechModel
		}
	} else {
		label += "not selected"
	}

	stateText, color := "● Loaded in RAM", m.theme.mint
	switch {
	case m.state == nil:
		stateText, color = "○ Service offline", m.theme.muted
	case !m.state.Runtime:
		stateText, color = "✖ Runtime not installed", m.theme.danger
	case !m.state.Model:
		stateText, color = "✖ Model not downloaded", m.theme.danger
	case m.state.Worker == transcribe.WorkerStarting:
		stateText, color = "○ Loading model into RAM…", m.theme.amber
	case m.state.Worker == transcribe.WorkerDead:
		stateText, color = "✖ Engine stopped", m.theme.danger
	}

	var out []string
	for _, l := range wrapWide(label+"  "+stateText, m.testPopupWidth()-5) {
		out = append(out, "  "+m.theme.base().Foreground(color).Render(l))
	}
	return out
}

// testSpeechResultBody builds the recognition pane: status line plus the
// complete, wrapped transcript.
func (m Model) testSpeechResultBody(wrapW int) []string {
	body := m.modelStatusLine()
	body = append(body, "")

	statusText := "● READY — Press Space to start speaking"
	statusColor := m.theme.mint
	if m.state != nil {
		switch m.state.Phase {
		case protocol.PhaseRecording:
			statusText = "● RECORDING AUDIO... Speak into mic, then press Space again when done."
			statusColor = m.theme.amber
		case protocol.PhaseTranscribing:
			statusText = "● TRANSCRIBING AUDIO WITH ONNX ENGINE…"
			statusColor = m.theme.violet
		case protocol.PhasePasting, protocol.PhaseSucceeded:
			statusText = "✓ RECOGNITION COMPLETE"
			statusColor = m.theme.mint
		case protocol.PhaseFailed:
			statusText = "✖ ERROR: " + shortError(m.state.LastError)
			statusColor = m.theme.danger
		}
	}
	for _, line := range wrapWide("Status: "+statusText, m.testPopupWidth()-5) {
		body = append(body, "  "+m.theme.base().Foreground(statusColor).Bold(true).Render(line))
	}
	body = append(body, "")

	body = append(body, "  "+m.theme.base().Foreground(m.theme.mint).Bold(true).Render("✔ Recognition Result:"))
	text := ""
	if m.testHasResult && m.state != nil {
		text = m.state.LastResult
		if text == "" {
			text = m.state.Transcript
		}
	}
	if text != "" {
		for _, line := range wrapWide("“"+text+"”", wrapW) {
			body = append(body, "    "+m.theme.base().Foreground(m.theme.mint).Bold(true).Render(line))
		}
	} else {
		body = append(body, "    "+m.theme.base().Foreground(m.theme.muted).Render("(Press Space to record audio)"))
	}
	body = append(body, "")
	return body
}

// testLogsBody builds the live trial-log pane: the section header plus
// every narrative line, wrapped.
func (m Model) testLogsBody(wrapW int) []string {
	var body []string
	body = append(body, "  "+m.theme.base().Foreground(m.theme.violet).Bold(true).Render("Trial Log:"))
	logLines := m.logs
	if len(logLines) == 0 {
		logLines = []string{"(no trial session yet — press Space to start)"}
	}
	for _, l := range logLines {
		for _, wrapped := range wrapWide(l, wrapW) {
			body = append(body, "    "+m.theme.base().Foreground(m.theme.muted).Render(wrapped))
		}
	}
	return body
}

func (m Model) testTranslationBox() string {
	wrapW := m.testPopupWidth() - 7 // indent 4 within the scrollbar gutter

	resultBody := m.testTranslationResultBody(wrapW)
	logsBody := m.testLogsBody(wrapW)

	footer := "  " + m.theme.base().Foreground(m.theme.focus).Render("Space/Enter/Shift+T") + " start/stop  ·  " +
		m.theme.base().Foreground(m.theme.focus).Render("Esc") + " cancel/exit"
	return m.testBox("Speech Translation Test", resultBody, logsBody, footer)
}

// testTranslationResultBody builds the recognition pane: model status, the
// original transcript, and the translated text.
func (m Model) testTranslationResultBody(wrapW int) []string {
	body := m.modelStatusLine()
	body = append(body, "")

	statusText := "● READY — Press Space to start speaking"
	statusColor := m.theme.mint
	if m.state != nil {
		switch m.state.Phase {
		case protocol.PhaseRecording:
			statusText = "● RECORDING AUDIO... Speak into mic, then press Space again when done."
			statusColor = m.theme.amber
		case protocol.PhaseTranscribing:
			statusText = "● TRANSCRIBING AUDIO…"
			statusColor = m.theme.violet
		case protocol.PhaseTranslating:
			statusText = "● TRANSLATING WITH LLM API…"
			statusColor = m.theme.violet
		case protocol.PhasePasting, protocol.PhaseSucceeded:
			statusText = "✓ TRANSLATION COMPLETE"
			statusColor = m.theme.mint
		case protocol.PhaseFailed:
			statusText = "✖ ERROR: " + shortError(m.state.LastError)
			statusColor = m.theme.danger
		}
	}
	for _, line := range wrapWide("Status: "+statusText, m.testPopupWidth()-5) {
		body = append(body, "  "+m.theme.base().Foreground(statusColor).Bold(true).Render(line))
	}
	body = append(body, "")

	body = append(body, "  "+m.theme.base().Foreground(m.theme.violet).Bold(true).Render("Recognition Output:"))
	transcript := ""
	if m.testHasResult && m.state != nil {
		transcript = m.state.Transcript
		if transcript == "" {
			transcript = m.state.LastResult
		}
	}
	if transcript != "" {
		for _, line := range wrapWide("“"+transcript+"”", wrapW) {
			body = append(body, "    "+m.theme.base().Foreground(m.theme.muted).Render(line))
		}
	} else {
		body = append(body, "    "+m.theme.base().Foreground(m.theme.muted).Render("(No speech recorded yet)"))
	}

	targetLang := m.cfg.Translation.TargetLanguage
	if targetLang == "" {
		targetLang = "Japanese"
	}
	body = append(body, "")
	body = append(body, "  "+m.theme.base().Foreground(m.theme.violet).Bold(true).Render("Translation Output ("+targetLang+"):"))
	if m.testHasResult && m.state != nil && m.state.LastResult != "" {
		for _, line := range wrapWide("“"+m.state.LastResult+"”", wrapW) {
			body = append(body, "    "+m.theme.base().Foreground(m.theme.mint).Bold(true).Render(line))
		}
	} else {
		body = append(body, "    "+m.theme.base().Foreground(m.theme.muted).Render("(Press Space to record and test translation)"))
	}
	body = append(body, "")
	return body
}

func shortError(err string) string {
	if err == "" {
		return "unknown error"
	}
	lines := strings.Split(err, "\n")
	return lines[0]
}

func padRight(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

// scrollbar renders a one-column vertical scrollbar for a list of n lines
// shown inside a viewport of v lines scrolled by offset (0 = top). It returns
// an empty string when everything already fits.
func scrollbar(n, v, offset int) string {
	if n <= v {
		return ""
	}
	const track = "│"
	const thumb = "█"
	if v < 1 {
		return ""
	}
	trackLen := v
	thumbLen := v * v / n
	if thumbLen < 1 {
		thumbLen = 1
	}
	maxOffset := n - v
	if maxOffset < 0 {
		maxOffset = 0
	}
	pos := 0
	if maxOffset > 0 {
		pos = offset * (v - thumbLen) / maxOffset
	}
	var b strings.Builder
	for i := 0; i < trackLen; i++ {
		if i >= pos && i < pos+thumbLen {
			b.WriteString(thumb)
		} else {
			b.WriteString(track)
		}
	}
	return b.String()
}

// wrapWide wraps s to width columns, breaking at word boundaries when
// possible and falling back to hard breaks for single over-long tokens or
// CJK text (which has no spaces to break at). Display width, not rune count,
// is used so wide glyphs never overflow.
func wrapWide(s string, width int) []string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return []string{s}
	}
	var lines []string
	for _, seg := range strings.Split(s, "\n") {
		lines = append(lines, wrapSegment(seg, width)...)
	}
	return lines
}

func wrapSegment(s string, width int) []string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return []string{s}
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	flushSpace := func() {
		// Trailing spaces are invisible at line ends and would break
		// width accounting on the next flush.
		out := strings.TrimRight(cur.String(), " ")
		if out != "" {
			lines = append(lines, out)
		}
		cur.Reset()
		curW = 0
	}

	for _, word := range strings.Fields(s) {
		w := lipgloss.Width(word)
		if curW > 0 && curW+1+w > width {
			// The next word does not fit: wrap before it.
			flushSpace()
		}
		if w > width {
			// Single word longer than the line: flush what we have, then
			// hard-break the word itself by display width.
			flushSpace()
			var chunk strings.Builder
			chunkW := 0
			for _, r := range word {
				rw := lipgloss.Width(string(r))
				if chunkW > 0 && chunkW+rw > width {
					lines = append(lines, chunk.String())
					chunk.Reset()
					chunkW = 0
				}
				chunk.WriteRune(r)
				chunkW += rw
			}
			if chunk.Len() > 0 {
				lines = append(lines, chunk.String())
			}
			continue
		}
		if curW > 0 {
			cur.WriteRune(' ')
			curW++
		}
		cur.WriteString(word)
		curW += w
	}
	flushSpace()
	if len(lines) == 0 {
		return []string{s}
	}
	return lines
}
