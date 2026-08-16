package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
	"github.com/iamcheyan/sasayaki/internal/translate"
)

// View renders the full screen: header, cards, footer.
func (m Model) View() string {
	if m.layout.compact {
		return m.compactView()
	}
	var content string
	content += m.header() + "\n\n"
	content += m.cards() + "\n\n"
	content += m.footer()
	// Place the content as one fixed-width block. Calling Place directly on
	// multiline text centers each short row independently, which makes a
	// title appear in the middle while the cards start at the left edge. The
	// reference layout centers the block but left-aligns every row within it.
	block := lipgloss.NewStyle().Width(m.layout.maxWidth).Render(content)
	// Keep the dashboard in the visual center of the terminal, as the other
	// tools in this family do. The content itself remains width-constrained by
	// the layout, so wide terminals get equal breathing room on both sides.
	rendered := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
	return m.overlayView(rendered)
}

func (m Model) header() string {
	title := m.theme.base().Foreground(m.theme.violet).Bold(true).Render("✦  sasayaki")
	muted := m.theme.base().Foreground(m.theme.muted)
	status := m.pill()
	// Musubi and shirabe share this exact hierarchy: brand/caption/status on
	// row one, the current object on row two, and a compact summary on row
	// three. Keep the text left aligned inside the shared 132-column block.
	line1 := title + "  " + muted.Render("local voice input")
	gap := m.layout.maxWidth - lipgloss.Width(line1) - lipgloss.Width(status)
	if gap < 1 {
		gap = 1
	}
	line1 += strings.Repeat(" ", gap) + status
	line2 := muted.Render("Editing ") + m.theme.base().Foreground(m.theme.focus).Bold(true).Render(m.headerFocus())
	line3 := muted.Render("local models  ·  on-device processing  ·  keyboard-first controls")
	return line1 + "\n" + line2 + "\n" + line3
}

func (m Model) headerFocus() string {
	labels := []string{
		"Local Speech Model", "Speech Language", "Model Specs & Storage",
		"Translation Toggle", "Translation Model", "Target Language",
		"Global Shortcut Guide", "Service Control", "Diagnostics & Logs",
	}
	if m.menuCursor >= 0 && m.menuCursor < len(labels) {
		return labels[m.menuCursor]
	}
	return labels[0]
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
	case m.state.Phase == protocol.PhaseTranslating:
		label, color = dot+" TRANSLATING", m.theme.violet
	case m.state.Phase == protocol.PhaseFailed:
		label, color = dot+" ERROR", m.theme.danger
	case !m.state.Model || !m.state.Runtime:
		label, color = "○ SETUP NEEDED", m.theme.amber
	}
	// Status is a compact badge, matching shirabe's right-hand health badge.
	// The label remains explicit so the color is only an accent, not the sole
	// source of state information.
	return m.theme.base().Foreground(lipgloss.Color("#000000")).Background(color).
		Bold(true).Render(" " + label + " ")
}

// cards renders the Master-Detail layout: Menu Panel on Left, Detail Panel on Right.
func (m Model) cards() string {
	leftBody := m.menuCardBody(m.leftCardWidth() - 2)
	rightTitle, rightBody, totalItems, visibleItems, topItem := m.detailCardContent()

	h := m.layout.cardHeight
	if h <= 0 {
		h = cardHeight
	}

	left := m.customCardFrame("CATEGORIES", leftBody, m.leftCardWidth(), h, m.activePanel == panelLeft)
	right := m.customCardFrameWithScroll(rightTitle, rightBody, m.rightCardWidth(), h, totalItems, visibleItems, topItem, m.activePanel == panelRight)

	if m.layout.sideBySide {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", 3), right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, left, "\n", right)
}

func (m Model) leftCardWidth() int {
	if !m.layout.sideBySide {
		return m.layout.cardWidth
	}
	w := 34
	if m.layout.maxWidth < 80 {
		w = 30
	}
	return w
}

func (m Model) rightCardWidth() int {
	if !m.layout.sideBySide {
		return m.layout.cardWidth
	}
	w := m.layout.maxWidth - m.leftCardWidth() - 3
	if w < 20 {
		w = 20
	}
	return w
}

func truncateWide(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	curW := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if curW+rw > width {
			break
		}
		b.WriteRune(r)
		curW += rw
	}
	return b.String()
}

func (m Model) customCardFrame(title, body string, width, targetHeight int, isActive bool) string {
	return m.customCardFrameWithScroll(title, body, width, targetHeight, 0, 0, 0, isActive)
}

func (m Model) customCardFrameWithScroll(title, body string, width, targetHeight, totalItems, visibleItems, topItem int, isActive bool) string {
	borderColor := m.theme.border
	if isActive {
		borderColor = m.theme.focus
	}
	inner := width - 2
	if inner < 10 {
		inner = 10
	}
	edge := m.theme.base().Foreground(borderColor)
	legend := m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" " + title + " ")
	rule := inner - lipgloss.Width(legend)
	if rule < 1 {
		rule = 1
	}
	var b strings.Builder
	b.WriteString(edge.Render("╭") + legend + edge.Render(strings.Repeat("─", rule)+"╮") + "\n")

	// Keep the content visually inset from the fieldset border.  The top and
	// bottom rows are intentional breathing room; the body gets the remaining
	// rows so every card still has the same fixed height.
	innerHeight := targetHeight - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	bodyHeight := innerHeight - 2
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	bodyLines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if bodyHeight == 0 {
		bodyLines = nil
	} else if len(bodyLines) > bodyHeight {
		bodyLines = bodyLines[:bodyHeight]
	} else {
		for len(bodyLines) < bodyHeight {
			bodyLines = append(bodyLines, "")
		}
	}
	lines := make([]string, 0, innerHeight)
	lines = append(lines, "") // top inset
	lines = append(lines, bodyLines...)
	lines = append(lines, "") // bottom inset
	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	}

	hasScrollbar := totalItems > visibleItems && visibleItems > 0 && totalItems > 0
	thumbPos := 0
	thumbSize := 1
	if hasScrollbar {
		maxTop := totalItems - visibleItems
		if maxTop > 0 {
			ratio := float64(topItem) / float64(maxTop)
			thumbPos = int(ratio * float64(innerHeight-1))
			if thumbPos >= innerHeight {
				thumbPos = innerHeight - 1
			}
		}
	}

	for idx, line := range lines {
		w := lipgloss.Width(line)
		if w > inner {
			line = truncateWide(line, inner)
			w = lipgloss.Width(line)
		}
		padding := inner - w
		if padding < 0 {
			padding = 0
		}

		rightBorder := edge.Render("│")
		if hasScrollbar {
			if idx >= thumbPos && idx < thumbPos+thumbSize {
				rightBorder = m.theme.base().Foreground(m.theme.focus).Bold(true).Render("█")
			} else {
				rightBorder = m.theme.base().Foreground(m.theme.muted).Render("│")
			}
		}

		b.WriteString(edge.Render("│") + line + strings.Repeat(" ", padding) + rightBorder + "\n")
	}
	b.WriteString(edge.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

func (m Model) menuCardBody(width int) string {
	var b strings.Builder

	categories := menuCategories()

	// The card height adapts to this list (see menuBodyHeight), so blank
	// separators between categories are kept and nothing is truncated.
	for _, cat := range categories {
		b.WriteString(m.section(cat.name, false))
		for _, item := range cat.items {
			isSelected := m.menuCursor == item.id
			pointer := "  "
			style := m.theme.base().Foreground(m.theme.muted)
			if isSelected {
				pointer = "• "
				// The family selection is a quiet, full-width dark-violet row
				// with a round bullet — never a bright inverse bar or triangle.
				selected := m.theme.base().Foreground(lipgloss.Color("#f4f1ff")).
					Background(m.theme.selected)
				line := pointer + item.label
				if width > lipgloss.Width(line) {
					line += strings.Repeat(" ", width-lipgloss.Width(line))
				}
				b.WriteString(selected.Render(line) + "\n")
				continue
			}
			b.WriteString(pointer + style.Render(item.label) + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// menuCategory is one settings-menu section. The layout measures this same
// data to size the cards, so the menu and its frame always agree.
type menuCategory struct {
	name  string
	items []menuItem
}

type menuItem struct {
	id    int
	label string
}

// menuCategories is the single source of truth for the settings menu.
func menuCategories() []menuCategory {
	return []menuCategory{
		{
			name: "SPEECH RECOGNITION",
			items: []menuItem{
				{0, "Local Speech Model"},
				{1, "Speech Language"},
				{2, "Model Specs & Storage"},
			},
		},
		{
			name: "TRANSLATION & LLM",
			items: []menuItem{
				{3, "Translation Toggle"},
				{4, "Translation Model"},
				{5, "Target Language"},
			},
		},
		{
			name: "SYSTEM & UTILS",
			items: []menuItem{
				{6, "Global Shortcut Guide"},
				{7, "Service Control"},
				{8, "Diagnostics & Logs"},
				{9, "Voice Wake Keys"},
			},
		},
	}
}

// menuCategoryItems is the per-category item count used by the layout's
// menuBodyHeight.
var menuCategoryItems = []int{3, 3, 4}

// installedBadge renders the per-model install-state pill from the cached
// snapshot. While the snapshot is still loading (nil) it shows a neutral
// marker instead of re-hashing the ONNX files on every frame.
func (m Model) installedBadge(id string) string {
	if m.installed == nil {
		return m.theme.base().Foreground(m.theme.muted).Render(" [···]")
	}
	if m.installed[id] {
		return m.theme.base().Foreground(m.theme.mint).Bold(true).Render(" [INSTALLED]")
	}
	return m.theme.base().Foreground(m.theme.amber).Render(" [NOT DOWNLOADED]")
}

func (m Model) detailCardContent() (string, string, int, int, int) {
	var b strings.Builder
	title := "DETAILS"
	totalItems, visibleItems, topItem := 0, 0, 0

	switch m.menuCursor {
	case 0:
		title = "LOCAL SPEECH MODEL CATALOG"
		if m.setupBusy {
			b.WriteString(m.theme.base().Foreground(m.theme.focus).Bold(true).Render("● Downloading & Setting Up Model…") + "\n\n")
			lines := m.setupLines
			if len(lines) > 4 {
				lines = lines[len(lines)-4:]
			}
			for _, line := range lines {
				for _, wrapped := range wrapWide(line, m.rightCardWidth()-6) {
					b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(wrapped) + "\n")
				}
			}
		} else {
			currentID := m.cfg.SpeechModel
			if currentID == "" && m.state != nil && m.state.SpeechModel != "" {
				currentID = m.state.SpeechModel
			}
			if currentID == "" {
				currentID = "sensevoice-int8"
			}

			for i, model := range transcribe.SpeechModels {
				isCursor := (i == m.modelCursor) && (m.activePanel == panelRight)
				isCurrent := model.ID == currentID

				pointer := "  "
				if isCursor {
					pointer = "• "
				}

				badges := ""
				if isCurrent {
					badges += m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" [CURRENT]")
				}
				badges += m.installedBadge(model.ID)

				numStr := fmt.Sprintf("[%d] ", i+1)
				titleStr := numStr + model.Label
				titleStyle := m.theme.base().Bold(true)
				if isCursor {
					titleStyle = m.theme.base().Foreground(m.theme.focus).Bold(true)
				}
				b.WriteString(pointer + titleStyle.Render(titleStr) + badges + "\n")

				sizeMB := model.ModelFile.Size / (1024 * 1024)
				specs := fmt.Sprintf("    Arch: %s · Size: %d MB · Languages: %s", model.Architecture, sizeMB, model.Languages)
				for _, wrapped := range wrapWide(specs, m.rightCardWidth()-6) {
					b.WriteString(m.theme.base().Foreground(m.theme.muted).Render(wrapped) + "\n")
				}

				descStyle := m.theme.base().Foreground(m.theme.muted)
				if isCursor {
					descStyle = m.theme.base()
				}
				b.WriteString("    " + descStyle.Render("“"+model.Description+"”") + "\n\n")
			}
		}

	case 1:
		title = "SPEECH RECOGNITION LANGUAGE"
		curLang := m.cfg.Language
		if curLang == "" {
			curLang = "auto"
		}
		langs := []struct{ code, name string }{
			{"auto", "auto (Auto Detect)"},
			{"zh", "zh (Chinese)"},
			{"en", "en (English)"},
			{"ja", "ja (Japanese)"},
			{"ko", "ko (Korean)"},
			{"yue", "yue (Cantonese)"},
		}
		for i, l := range langs {
			isCursor := (i == m.langCursor) && (m.activePanel == panelRight)
			isCurrent := l.code == curLang
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			badge := ""
			if isCurrent {
				badge = m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" [CURRENT]")
			}
			numStr := fmt.Sprintf("[%d] ", i+1)
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(numStr+l.name) + badge + "\n")
		}

	case 2:
		title = "MODEL SPECS & DISK STORAGE"
		b.WriteString(m.section("ENVIRONMENT & SPECS", true))
		id := "sensevoice-int8"
		if m.state != nil && m.state.SpeechModel != "" {
			id = m.state.SpeechModel
		}
		model, _ := transcribe.SpeechModelByID(id)
		b.WriteString("  Active Model: " + id + " (" + model.Architecture + ")\n")
		b.WriteString("  Model Dir:    " + transcribe.ModelDir(m.paths, id) + "\n\n")
		b.WriteString(m.section("MAINTENANCE ACTIONS", false))
		actions := []string{
			"[1] Inspect SHA-256 Checksums & Paths",
			"[2] Verify Python Virtual Environment (venv)",
			"[3] Repair Runtime & Re-download Missing Files",
		}
		for i, act := range actions {
			isCursor := (i == m.specCursor) && (m.activePanel == panelRight)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(act) + "\n")
		}

	case 3:
		title = "LLM TRANSLATION TOGGLE"
		b.WriteString(m.section("TRANSLATION MODE", true))
		enabled := m.cfg.Translation.Enabled
		modes := []struct {
			enabled     bool
			label, desc string
		}{
			{false, "[1] Disabled (Off)", "Speech recognition only; pastes raw text directly."},
			{true, "[2] Enabled (On)", "Automatically translates text via LLM before pasting."},
		}
		for i, mode := range modes {
			isCursor := (i == m.transToggleCursor) && (m.activePanel == panelRight)
			isCurrent := mode.enabled == enabled
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			badge := ""
			if isCurrent {
				badge = m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" [CURRENT]")
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(mode.label) + badge + "\n")
			b.WriteString("    " + m.theme.base().Foreground(m.theme.muted).Render(mode.desc) + "\n\n")
		}

	case 4:
		title = "TRANSLATION LLM MODEL"
		b.WriteString(m.section("OPENCODE & LLM MODELS", true))
		openCodeModels := translate.LoadOpenCodeModels()
		type modelItem struct {
			id, name, endpoint, apiKey string
		}
		var models []modelItem
		if len(openCodeModels) > 0 {
			for _, om := range openCodeModels {
				models = append(models, modelItem{
					id:       om.ID,
					name:     om.FullName,
					endpoint: om.BaseURL,
					apiKey:   om.APIKey,
				})
			}
		} else {
			models = []modelItem{
				{"deepseek-chat", "DS/deepseek-v4-flash", "https://api.deepseek.com/v1", ""},
				{"gpt-4o-mini", "openai/gpt-4o-mini", "https://api.openai.com/v1", ""},
				{"opencode", "opencode-go/deepseek-v4-pro", "http://localhost:8080/v1", ""},
			}
		}

		curModel := m.cfg.Translation.Model
		curURL := m.cfg.Translation.BaseURL
		if curModel == "" {
			curModel = "deepseek-v4-flash"
		}

		// Fixed viewport: each model = 2 lines (name + endpoint).  The shared
		// card frame reserves one row of breathing room above and below content.
		// We always emit exactly the available body height so the frame remains
		// fixed while scrolling.
		viewLines := m.layout.cardHeight - 4
		if viewLines < 1 {
			viewLines = cardHeight - 4
		}
		modelsPerPage := viewLines / 2
		if modelsPerPage < 1 {
			modelsPerPage = 1
		}
		totalModels := len(models)

		// Compute viewport start so the cursor is always visible.
		startIdx := 0
		if m.transModelCursor >= modelsPerPage {
			startIdx = m.transModelCursor - modelsPerPage + 1
		}
		endIdx := startIdx + modelsPerPage
		if endIdx > totalModels {
			endIdx = totalModels
			startIdx = endIdx - modelsPerPage
			if startIdx < 0 {
				startIdx = 0
			}
		}

		// Emit exactly modelsPerPage item-slots (2 lines each).
		for slot := 0; slot < modelsPerPage; slot++ {
			i := startIdx + slot
			if i >= totalModels {
				// Pad with blank lines to keep height fixed.
				b.WriteString("\n\n")
				continue
			}
			tm := models[i]
			isCursor := (i == m.transModelCursor) && (m.activePanel == panelRight)
			isCurrent := (tm.id == curModel) && (curURL == "" || strings.HasPrefix(curURL, tm.endpoint) || strings.HasPrefix(tm.endpoint, curURL))
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			badge := ""
			if isCurrent {
				badge = m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" [CURRENT]")
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(tm.name) + badge + "\n")
			b.WriteString("    " + m.theme.base().Foreground(m.theme.muted).Render("Endpoint: "+tm.endpoint) + "\n")
		}

		// Pass scroll info so the frame can draw the scrollbar thumb.
		totalItems = totalModels
		visibleItems = modelsPerPage
		topItem = startIdx

	case 5:
		title = "TARGET TRANSLATION LANGUAGE"
		b.WriteString(m.section("SELECT TARGET LANGUAGE", true))
		curTarget := m.cfg.Translation.TargetLanguage
		if curTarget == "" {
			curTarget = "Japanese"
		}
		targets := []string{"Japanese", "English", "Chinese", "Korean", "Spanish", "French", "German"}
		for i, t := range targets {
			isCursor := (i == m.targetCursor) && (m.activePanel == panelRight)
			isCurrent := strings.EqualFold(t, curTarget)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			badge := ""
			if isCurrent {
				badge = m.theme.base().Foreground(m.theme.violet).Bold(true).Render(" [CURRENT]")
			}
			numStr := fmt.Sprintf("[%d] ", i+1)
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(numStr+t) + badge + "\n")
		}

	case 6:
		title = "GLOBAL SHORTCUT GUIDE"
		b.WriteString(m.section("DESKTOP ENVIRONMENT GUIDES", true))
		guides := []struct{ env, command string }{
			{"[1] Hyprland", "bind = SUPER, V, exec, sasayaki toggle"},
			{"[2] Sway", "bindsym $mod+v exec sasayaki toggle"},
			{"[3] GNOME", "Settings -> Keyboard -> Custom Shortcuts"},
			{"[4] KDE Plasma", "System Settings -> Shortcuts -> Add Command"},
		}
		for i, g := range guides {
			isCursor := (i == m.shortcutCursor) && (m.activePanel == panelRight)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(g.env) + "\n")
			b.WriteString("    " + m.theme.base().Foreground(m.theme.muted).Render(g.command) + "\n\n")
		}

	case 7:
		title = "SYSTEMD SERVICE CONTROL"
		b.WriteString(m.section("SERVICE MANAGEMENT", true))
		actions := []struct{ name, desc string }{
			{"[1] Service Status & Health", "Active (systemd user unit sasayaki.service)"},
			{"[2] Disable / Stop Service", "Stops Sasayaki service from running in background."},
			{"[3] Restart Service", "Restarts the daemon (for quick recovery)."},
		}
		for i, act := range actions {
			isCursor := (i == m.serviceCursor) && (m.activePanel == panelRight)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(act.name) + "\n")
			b.WriteString("    " + m.theme.base().Foreground(m.theme.muted).Render(act.desc) + "\n\n")
		}

	case 9:
		title = "VOICE WAKE KEYS"
		b.WriteString(m.section("WAKE KEYS (tap alone to toggle voice)", true))
		rows := []struct {
			label string
			on    bool
		}{
			{"CapsLock", m.cfg.WakeKeys.CapsLock},
			{"LeftCtrl", m.cfg.WakeKeys.LeftCtrl},
			{"RightCtrl", m.cfg.WakeKeys.RightCtrl},
		}
		for i, row := range rows {
			isCursor := (i == m.wakeCursor) && (m.activePanel == panelRight)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			state := "OFF"
			stateColor := m.theme.muted
			if row.on {
				state = "ON"
				stateColor = m.theme.focus
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			mark := "["
			if row.on {
				mark = "[x"
			}
			mark += "]"
			b.WriteString(pointer + m.theme.base().Foreground(stateColor).Render(mark) + " " +
				style.Render(fmt.Sprintf("[%d] %s wake", i+1, row.label)) + "  " +
				m.theme.base().Foreground(stateColor).Render(state) + "\n")
		}
		b.WriteString("\n" + m.section("HOW IT WORKS", false))
		lines := []string{
			"Any combination may be on; all may be off.",
			"Binds are keycode-based, so they follow the physical key",
			"even across keyd swaps and XKB remaps.",
			"Release-only + transparent: a key only wakes on a bare tap;",
			"chords like Ctrl+C keep their normal meaning.",
		}
		for _, l := range lines {
			b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(l) + "\n")
		}
	case 8:
		title = "DIAGNOSTICS & SERVICE LOGS"
		b.WriteString(m.section("DIAGNOSTIC & LOG TOOLS", true))
		actions := []struct{ name, desc string }{
			{"[1] Run System Environment Diagnostic", "Runs self-checks: Python venv, ONNX model checksums, systemd service."},
			{"[2] Open Live Systemd Service Logs", "Opens full-screen scrollable log viewer for sasayaki daemon."},
		}
		for i, act := range actions {
			isCursor := (i == m.diagCursor) && (m.activePanel == panelRight)
			pointer := "  "
			if isCursor {
				pointer = "• "
			}
			style := m.theme.base()
			if isCursor {
				style = m.theme.base().Foreground(m.theme.focus).Bold(true)
			}
			b.WriteString(pointer + style.Render(act.name) + "\n")
			b.WriteString("    " + m.theme.base().Foreground(m.theme.muted).Render(act.desc) + "\n\n")
		}

		b.WriteString(m.section("RECENT LOG PREVIEW", false))
		lines := m.logs
		if len(lines) == 0 {
			lines = []string{"(select [2] or press Enter to load live logs)"}
		}
		if len(lines) > 3 {
			lines = lines[len(lines)-3:]
		}
		for _, line := range lines {
			for _, wrapped := range wrapWide(line, m.rightCardWidth()-6) {
				b.WriteString("  " + m.theme.base().Foreground(m.theme.muted).Render(wrapped) + "\n")
			}
		}
	}

	return title, b.String(), totalItems, visibleItems, topItem
}

// section uses the same quiet, label-only section treatment as musubi. The
// current section becomes violet; it does not gain a decorative pointer.
func (m Model) section(name string, focused bool) string {
	pointer := " "
	style := m.theme.base().Foreground(m.theme.muted).Bold(true)
	if focused {
		style = m.theme.base().Foreground(m.theme.violet).Bold(true)
	}
	return pointer + style.Render(name) + "\n"
}

func langName(code string) string {
	switch code {
	case "auto":
		return "auto (Auto Detect)"
	case "zh":
		return "zh (Chinese)"
	case "en":
		return "en (English)"
	case "ja":
		return "ja (Japanese)"
	case "ko":
		return "ko (Korean)"
	case "yue":
		return "yue (Cantonese)"
	default:
		return code
	}
}

// footer is the centered bottom line: transient notice while fresh, else
// the key hints.
func (m Model) footer() string {
	text := m.footerKeys()
	if m.notice != "" && time.Now().Before(m.noticeUntil) {
		text = m.theme.base().Foreground(m.theme.mint).Bold(true).Render(m.notice)
	}
	return lipgloss.Place(m.layout.maxWidth, 1, lipgloss.Center, lipgloss.Center,
		text)
}

// footerKeys uses the same keycap grammar as shirabe: a light violet key
// label followed by a dark description chip. It makes the actions scannable
// without relying on color alone.
func (m Model) footerKeys() string {
	keyStyle := m.theme.base().Foreground(lipgloss.Color("#17151f")).Background(m.theme.violet).Bold(true)
	descStyle := m.theme.base().Foreground(m.theme.muted).Background(m.theme.surface)
	items := []struct{ key, desc string }{
		{"←/→", "switch"}, {"↑/↓", "navigate"}, {"Enter", "select"},
		{"t", "speech test"}, {"Shift+T", "translation test"},
		{"r", "repair"}, {"?", "help"}, {"q", "quit"},
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, keyStyle.Render(" "+item.key+" ")+descStyle.Render(" "+item.desc+" "))
	}
	return strings.Join(parts, " ")
}

// compactView is the reduced view for very small terminals.
func (m Model) compactView() string {
	title := m.theme.base().Foreground(m.theme.violet).Bold(true).Render("✦ sasayaki")
	line := title + "   " + m.pill()
	hint := m.theme.base().Foreground(m.theme.muted).Render("t test · m models · r repair · d diagnose · ? help · q quit")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		line+"\n"+hint)
}
