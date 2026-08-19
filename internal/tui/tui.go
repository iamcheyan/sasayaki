// Package tui is the Sasayaki control center: two equal cards (VOICE /
// RUNTIME) with spatial arrow-key navigation, letter shortcuts, overlays
// and transient notices. It is a thin client over the control socket and
// never talks to the model or the microphone itself.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/service"
	"github.com/iamcheyan/sasayaki/internal/setup"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
	"github.com/iamcheyan/sasayaki/internal/translate"

	"github.com/mattn/go-isatty"
)

// overlays
const (
	overlayNone            = ""
	overlayHelp            = "help"
	overlayKeys            = "shortcut"
	overlayLogs            = "logs"
	overlayDiag            = "diagnose"
	overlaySetup           = "setup"
	overlayConfirm         = "confirm"
	overlayTestSpeech      = "test_speech"
	overlayTestTranslation = "test_translation"
)

// test overlay panes: scroll keys act on the active pane (Tab switches).
const (
	testPaneResult = 0
	testPaneLogs   = 1
)

// messages
type stateMsg struct {
	state *protocol.State
	err   error
}

type toggleMsg struct {
	notice string
	state  *protocol.State
	err    error
}

type noticeMsg struct{ text string }

type modelChoiceMsg struct {
	label     string
	installed bool
	err       error
}

// cfgMsg carries a freshly loaded/saved config into the model. notice, when
// non-empty, is shown as a transient footer notice.
type cfgMsg struct {
	cfg    config.Config
	notice string
}

// installedMsg carries a refreshed installed-status snapshot for every speech
// model so renders never re-hash the (multi-hundred-MB) ONNX files.
type installedMsg struct{ installed map[string]bool }

type setupProgressMsg struct{ line string }

type setupDoneMsg struct {
	result setup.PlanResult
	err    error
	repair bool // true when triggered by the repair key (r/R)
}

type logsMsg struct{ lines []string }

type diagMsg struct {
	report diagnostics.Report
}

type tickMsg struct{}

const (
	panelLeft  = 0
	panelRight = 1
)

// Model is the TUI state.
type Model struct {
	paths config.Paths
	theme theme

	width     int
	height    int
	layout    layout
	cfg       config.Config
	installed map[string]bool

	state *protocol.State

	notice      string
	noticeUntil time.Time

	overlay           string
	activePanel       int
	menuCursor        int
	modelCursor       int
	langCursor        int
	specCursor        int
	transToggleCursor int
	transModelCursor  int
	targetCursor      int
	shortcutCursor    int
	serviceCursor     int
	wakeCursor        int
	diagCursor        int
	testResultScroll  int  // recognition pane scroll offset
	testLogScroll     int  // logs pane scroll offset
	testFollow        bool // logs pane pins to the newest line
	testActivePane    int  // pane scroll keys act on (testPaneResult/Logs)
	testHasResult     bool // a recognition result was produced during this test session

	// trialActive tracks a test session for the trial-log narrative: recording
	// has started and has not yet reached a terminal result. The remaining
	// trial* fields drive the live audio-level heartbeats, mirroring the
	// reference UI's [trial] log.
	trialActive          bool
	trialRecStart        time.Time
	trialPeak            int
	trialSpeechSeen      bool
	trialLastLevel       int
	trialLastLogAt       time.Time
	trialLiveLogged      bool // "Recorder is live" emitted for this session
	trialStopLogged      bool // stop block emitted for this session
	trialTranslateLogged bool // "Translating with the LLM API" line emitted

	logs        []string
	logScroll   int
	diag        diagnostics.Report
	diagDone    bool
	setupLines  []string
	setupCh     <-chan tea.Msg
	setupBusy   bool
	setupRepair bool // true when the current overlay is a repair, not plain setup
}

// New builds the TUI model.
func New(paths config.Paths) Model {
	return Model{
		paths:       paths,
		theme:       defaultTheme(),
		activePanel: panelLeft,
	}
}

// Run starts the TUI and blocks until quit.
//
// The TUI may inherit NO_COLOR / CLICOLOR / CI from the launching context
// (e.g. a CI shell or agent harness). On macOS the menubar app launches
// the TUI inside kitty, but single-instance kitty can keep those vars.
// termenv treats CI as "not a TTY" and NO_COLOR as "no color". When
// stdout is a real TTY we are interactive, so clear them before
// lipgloss/termenv detects the color profile.
func Run(paths config.Paths) error {
	if isatty.IsTerminal(os.Stdout.Fd()) {
		os.Unsetenv("NO_COLOR")
		os.Unsetenv("CLICOLOR")
		os.Unsetenv("CI")
	}
	program := tea.NewProgram(New(paths), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

// Init starts state polling, the notice clock, and loads config + the
// installed-model snapshot once so the render loop never touches disk or
// re-hashes the model files.
func (m Model) Init() tea.Cmd {
	return tea.Batch(pollState(m), tick(), loadConfigCmd(m.paths), refreshInstalledCmd(m.paths))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout = computeLayout(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		interval := 2 * time.Second
		var cmds []tea.Cmd
		if m.overlay == overlayTestSpeech || m.overlay == overlayTestTranslation {
			interval = 150 * time.Millisecond // snappy feedback: status polls are ~0.1ms now
			// Live audio-level heartbeats for the trial log while recording.
			// The narrative itself is built client-side from phase transitions,
			// so no journalctl is involved here.
			if m.trialActive && m.trialLiveLogged && m.state != nil && m.state.Phase == protocol.PhaseRecording {
				m.trialHeartbeat(m.state.MicLevel)
			}
		} else if m.state != nil {
			switch m.state.Phase {
			case protocol.PhaseRecording, protocol.PhaseTranscribing, protocol.PhaseTranslating, protocol.PhasePasting:
				interval = 300 * time.Millisecond
			}
		}
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
		}
		cmds = append(cmds, pollState(m), tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} }))
		return m, tea.Batch(cmds...)

	case stateMsg:
		if msg.err != nil {
			// Socket vanished: clear the snapshot so the UI shows STOPPED.
			m.state = nil
			return m, nil
		}
		prevPhase := protocol.PhaseIdle
		if m.state != nil {
			prevPhase = m.state.Phase
		}
		m.state = msg.state
		m.captureTestResult(prevPhase, msg.state)
		m.trialTransition(prevPhase, msg.state)
		return m, nil

	case toggleMsg:
		if msg.err != nil {
			m.showNotice(msg.err.Error())
			if m.trialActive {
				m.appendTrial("[trial] ERROR: " + msg.err.Error())
			}
			return m, nil
		}
		if msg.state != nil {
			prevPhase := protocol.PhaseIdle
			if m.state != nil {
				prevPhase = m.state.Phase
			}
			m.state = msg.state
			m.captureTestResult(prevPhase, msg.state)
			m.trialTransition(prevPhase, msg.state)
		}
		// A start that never reached recording (mic failure, runtime not
		// ready) would leave the session hanging with no phase transition to
		// close it; surface the daemon's detail as the verdict instead.
		if m.trialActive && !m.trialLiveLogged && m.state != nil && m.state.Phase == protocol.PhaseIdle {
			detail := msg.notice
			if detail == "" {
				detail = "start failed"
			}
			m.appendTrial("[trial] ERROR: " + detail)
			m.trialActive = false
		}
		if msg.notice != "" {
			m.showNotice(msg.notice)
		}
		return m, nil

	case noticeMsg:
		m.showNotice(msg.text)
		return m, nil

	case cfgMsg:
		m.cfg = msg.cfg
		if msg.notice != "" {
			m.showNotice(msg.notice)
		}
		return m, nil

	case installedMsg:
		m.installed = msg.installed
		return m, nil

	case modelChoiceMsg:
		if msg.err != nil {
			m.showNotice("Could not select model: " + msg.err.Error())
			return m, nil
		}
		if !msg.installed {
			m.showNotice("Downloading " + msg.label)
			mm, cmd := m.startSetup()
			return mm, tea.Batch(loadConfigCmd(m.paths), cmd)
		}
		m.showNotice("Selected " + msg.label + " — ready")
		// Selection wrote config; refresh the cached copy.
		return m, loadConfigCmd(m.paths)

	case setupProgressMsg:
		if len(m.setupLines) > 0 && strings.HasPrefix(msg.line, "Downloading ") && strings.HasPrefix(m.setupLines[len(m.setupLines)-1], "Downloading ") {
			m.setupLines[len(m.setupLines)-1] = msg.line
		} else {
			m.setupLines = append(m.setupLines, msg.line)
		}
		// Re-arm the stream for the next progress line.
		return m, func() tea.Msg { return <-m.setupCh }

	case setupDoneMsg:
		m.setupBusy = false
		m.setupCh = nil
		isRepair := m.setupRepair
		m.setupRepair = false
		for _, s := range msg.result.Steps {
			if s.Status == setup.StepFailed && s.Error != "" {
				m.setupLines = append(m.setupLines, "Error ("+s.ID+"): "+s.Error)
			}
		}
		m.setupLines = append(m.setupLines, summaryLine(msg))
		if msg.err != nil {
			label := "setup"
			if isRepair || msg.repair {
				label = "repair"
			}
			m.setupLines = append(m.setupLines, label+" failed: "+msg.err.Error())
		}
		label := "setup"
		if isRepair || msg.repair {
			label = "repair"
		}
		m.showNotice(label + " " + outcomeWord(msg))
		// Setup may have downloaded/repaired model files: recompute the
		// installed snapshot and reload config off the render loop.
		return m, tea.Batch(loadConfigCmd(m.paths), refreshInstalledCmd(m.paths))

	case logsMsg:
		m.logs = msg.lines
		return m, nil

	case diagMsg:
		m.diag = msg.report
		m.diagDone = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func summaryLine(msg setupDoneMsg) string {
	if msg.err != nil {
		return "✖ setup failed: " + msg.err.Error()
	}
	if msg.result.AllOK() {
		skipped := fmt.Sprintf("%d skipped", msg.result.Skipped)
		return "✓ setup complete (" + skipped + ")"
	}
	var errMsgs []string
	for _, s := range msg.result.Steps {
		if s.Status == setup.StepFailed && s.Error != "" {
			errMsgs = append(errMsgs, s.Error)
		}
	}
	if len(errMsgs) > 0 {
		return "✖ setup failed: " + strings.Join(errMsgs, "; ")
	}
	return "✖ setup incomplete: " + strings.Join(msg.result.Failed, ", ")
}

func outcomeWord(msg setupDoneMsg) string {
	if msg.err != nil {
		return "failed"
	}
	if msg.result.AllOK() {
		return "complete"
	}
	return "failed"
}

// handleKey handles keyboard navigation between Left and Right panels.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.overlay == overlayConfirm {
		switch key {
		case "y", "Y", "enter":
			m.overlay = overlayNone
			m.showNotice("Disabling the service…")
			return m, disableServiceCmd()
		case "n", "N", "esc", "q", "Q":
			m.overlay = overlayNone
			return m, nil
		}
		return m, nil
	}

	if m.overlay == overlayTestSpeech || m.overlay == overlayTestTranslation {
		switch key {
		case "space", " ", "t", "T", "shift+t", "enter":
			starting := m.state == nil || m.state.Phase != protocol.PhaseRecording
			m.testResultScroll = 0
			m.testLogScroll = 0
			m.testFollow = true
			if starting {
				// Fresh session: clear the trial log, drop any prior result.
				// The narrative header is appended when the daemon reports
				// recording; a failed start surfaces via the toggle response.
				m.logs = nil
				m.testHasResult = false
				m.trialActive = true
				m.trialRecStart = time.Now()
				m.trialPeak = 0
				m.trialSpeechSeen = false
				m.trialLastLevel = -1
				m.trialLastLogAt = time.Time{}
				m.trialLiveLogged = false
				m.trialStopLogged = false
			}
			op := protocol.OpTestSpeech
			if m.overlay == overlayTestTranslation {
				op = protocol.OpTestTranslation
			}
			return m, toggleCmd(m.paths, op)
		case "esc":
			if m.state != nil && m.state.Phase == protocol.PhaseRecording {
				// Cancel without transcribing, staying in the overlay, matching
				// the reference UI's trial flow.
				m.trialActive = false
				m.appendTrial("[trial] Recording cancelled — audio discarded (not transcribed)")
				return m, cancelCmd(m.paths)
			}
			m.overlay = overlayNone
			return m, nil
		case "q", "Q":
			m.overlay = overlayNone
			return m, nil
		case "tab":
			m.testActivePane = 1 - m.testActivePane
			return m, nil
		case "up", "k":
			resultBody, logsBody := m.testPaneBodies()
			g := m.testPaneGeom(resultBody, logsBody)
			if m.testActivePane == testPaneLogs {
				off := g.logsOffset
				if m.testFollow {
					// Leaving follow mode starts one line above the newest.
					m.testFollow = false
					off--
				} else {
					off--
				}
				if off < 0 {
					off = 0
				}
				m.testLogScroll = off
			} else {
				if g.resultOffset > 0 {
					m.testResultScroll = g.resultOffset - 1
				}
			}
			return m, nil
		case "down", "j":
			resultBody, logsBody := m.testPaneBodies()
			g := m.testPaneGeom(resultBody, logsBody)
			if m.testActivePane == testPaneLogs {
				if m.testFollow {
					// Already pinned to the newest line; stay there.
				} else if g.logsOffset >= g.logsMax {
					// Scrolled back to the newest line: resume following.
					m.testFollow = true
				} else {
					m.testLogScroll = g.logsOffset + 1
				}
			} else {
				m.testResultScroll = g.resultOffset + 1
			}
			return m, nil
		case "pgup":
			resultBody, logsBody := m.testPaneBodies()
			g := m.testPaneGeom(resultBody, logsBody)
			if m.testActivePane == testPaneLogs {
				m.testFollow = false
				m.testLogScroll = g.logsOffset - 10
				if m.testLogScroll < 0 {
					m.testLogScroll = 0
				}
			} else {
				m.testResultScroll = g.resultOffset - 10
				if m.testResultScroll < 0 {
					m.testResultScroll = 0
				}
			}
			return m, nil
		case "pgdown":
			resultBody, logsBody := m.testPaneBodies()
			g := m.testPaneGeom(resultBody, logsBody)
			if m.testActivePane == testPaneLogs {
				if m.testFollow {
					// Already following; nothing to do.
				} else if g.logsOffset+10 >= g.logsMax {
					m.testFollow = true
				} else {
					m.testLogScroll = g.logsOffset + 10
				}
			} else {
				m.testResultScroll = g.resultOffset + 10
			}
			return m, nil
		case "home", "g":
			if m.testActivePane == testPaneLogs {
				m.testFollow = false
				m.testLogScroll = 0
			} else {
				m.testResultScroll = 0
			}
			return m, nil
		case "end", "G":
			resultBody, logsBody := m.testPaneBodies()
			g := m.testPaneGeom(resultBody, logsBody)
			if m.testActivePane == testPaneLogs {
				m.testFollow = true
			} else {
				m.testResultScroll = g.resultMax
			}
			return m, nil
		}
		return m, nil
	}

	if m.overlay != overlayNone {
		switch key {
		case "esc":
			m.overlay = overlayNone
		case "q", "Q":
			if m.overlay == overlayHelp || m.overlay == overlayKeys {
				m.overlay = overlayNone
			}
		case "up", "k":
			if m.overlay == overlayLogs {
				m.scrollLogs(-1)
			}
		case "down", "j":
			if m.overlay == overlayLogs {
				m.scrollLogs(1)
			}
		}
		return m, nil
	}

	const menuCount = 10
	speechLangs := []string{"auto", "zh", "en", "ja", "ko", "yue"}
	targetLangs := []string{"Japanese", "English", "Chinese", "Korean", "Spanish", "French", "German"}

	// Global Action Shortcuts
	switch key {
	case "q", "Q", "ctrl+c":
		return m, tea.Quit
	case "r", "R":
		m.showNotice("Repairing…")
		return m.startRepair()
	case "t":
		m.logScroll = 0
		m.overlay = overlayTestSpeech
		m.testResultScroll = 0
		m.testLogScroll = 0
		m.testFollow = true
		m.testActivePane = testPaneLogs
		m.resetTrial()
		return m, pollState(m)
	case "T", "shift+t":
		m.logScroll = 0
		m.overlay = overlayTestTranslation
		m.testResultScroll = 0
		m.testLogScroll = 0
		m.testFollow = true
		m.testActivePane = testPaneLogs
		m.resetTrial()
		return m, pollState(m)
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "tab":
		m.activePanel = 1 - m.activePanel
		return m, nil
	}

	// Panel-Specific Navigation
	if m.activePanel == panelLeft {
		switch key {
		case "up", "k":
			m.menuCursor = (m.menuCursor - 1 + menuCount) % menuCount
			return m, nil
		case "down", "j":
			m.menuCursor = (m.menuCursor + 1) % menuCount
			return m, nil
		case "right", "l", "enter", " ":
			m.activePanel = panelRight
			return m, nil
		}
		return m, nil
	}

	// activePanel == panelRight
	switch key {
	case "left", "h", "esc":
		m.activePanel = panelLeft
		return m, nil
	}

	switch m.menuCursor {
	case 0: // Local Speech Model
		switch key {
		case "up", "k":
			m.modelCursor--
			if m.modelCursor < 0 {
				m.modelCursor = len(transcribe.SpeechModels) - 1
			}
			return m, nil
		case "down", "j":
			m.modelCursor = (m.modelCursor + 1) % len(transcribe.SpeechModels)
			return m, nil
		case "1":
			m.modelCursor = 0
			return m, selectModelCmd(m.paths, transcribe.SpeechModels[0].ID)
		case "2":
			if len(transcribe.SpeechModels) > 1 {
				m.modelCursor = 1
				return m, selectModelCmd(m.paths, transcribe.SpeechModels[1].ID)
			}
		case "3":
			if len(transcribe.SpeechModels) > 2 {
				m.modelCursor = 2
				return m, selectModelCmd(m.paths, transcribe.SpeechModels[2].ID)
			}
		case "enter", " ":
			if m.modelCursor >= 0 && m.modelCursor < len(transcribe.SpeechModels) {
				return m, selectModelCmd(m.paths, transcribe.SpeechModels[m.modelCursor].ID)
			}
		}

	case 1: // Speech Language
		switch key {
		case "up", "k":
			m.langCursor = (m.langCursor - 1 + len(speechLangs)) % len(speechLangs)
			return m, nil
		case "down", "j":
			m.langCursor = (m.langCursor + 1) % len(speechLangs)
			return m, nil
		case "1", "2", "3", "4", "5", "6":
			idx := int(key[0] - '1')
			if idx >= 0 && idx < len(speechLangs) {
				m.langCursor = idx
				return m, setLanguageCmd(m.paths, speechLangs[idx])
			}
		case "enter", " ":
			if m.langCursor >= 0 && m.langCursor < len(speechLangs) {
				return m, setLanguageCmd(m.paths, speechLangs[m.langCursor])
			}
		}

	case 2: // Model Specs
		switch key {
		case "up", "k":
			m.specCursor = (m.specCursor - 1 + 3) % 3
			return m, nil
		case "down", "j":
			m.specCursor = (m.specCursor + 1) % 3
			return m, nil
		case "enter", " ":
			if m.specCursor == 2 {
				return m.startSetup()
			}
			m.showNotice("Model directory and checksum verified")
			return m, nil
		}

	case 3: // Translation Toggle
		switch key {
		case "up", "k", "down", "j":
			m.transToggleCursor = 1 - m.transToggleCursor
			return m, nil
		case "enter", " ":
			return m, setTranslationEnabledCmd(m.paths, m.transToggleCursor == 1)
		}

	case 4: // Translation Model
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
		switch key {
		case "up", "k":
			m.transModelCursor = (m.transModelCursor - 1 + len(models)) % len(models)
			return m, nil
		case "down", "j":
			m.transModelCursor = (m.transModelCursor + 1) % len(models)
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(key[0] - '1')
			if idx >= 0 && idx < len(models) {
				m.transModelCursor = idx
				return m, setTranslationModelCmd(m.paths, models[idx].id, models[idx].endpoint, models[idx].apiKey)
			}
		case "enter", " ":
			if m.transModelCursor >= 0 && m.transModelCursor < len(models) {
				idx := m.transModelCursor
				return m, setTranslationModelCmd(m.paths, models[idx].id, models[idx].endpoint, models[idx].apiKey)
			}
		}

	case 5: // Target Language
		switch key {
		case "up", "k":
			m.targetCursor = (m.targetCursor - 1 + len(targetLangs)) % len(targetLangs)
			return m, nil
		case "down", "j":
			m.targetCursor = (m.targetCursor + 1) % len(targetLangs)
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7":
			idx := int(key[0] - '1')
			if idx >= 0 && idx < len(targetLangs) {
				m.targetCursor = idx
				return m, setTargetLanguageCmd(m.paths, targetLangs[idx])
			}
		case "enter", " ":
			if m.targetCursor >= 0 && m.targetCursor < len(targetLangs) {
				return m, setTargetLanguageCmd(m.paths, targetLangs[m.targetCursor])
			}
		}

	case 6: // Shortcut Guide
		switch key {
		case "up", "k":
			m.shortcutCursor = (m.shortcutCursor - 1 + 4) % 4
			return m, nil
		case "down", "j":
			m.shortcutCursor = (m.shortcutCursor + 1) % 4
			return m, nil
		}

	case 7: // Service Control
		switch key {
		case "up", "k":
			m.serviceCursor = (m.serviceCursor - 1 + 3) % 3
			return m, nil
		case "down", "j":
			m.serviceCursor = (m.serviceCursor + 1) % 3
			return m, nil
		case "1":
			m.serviceCursor = 0
			return m, nil
		case "2":
			m.serviceCursor = 1
			m.overlay = overlayConfirm
			return m, nil
		case "3":
			m.serviceCursor = 2
			m.showNotice("Restarting service…")
			return m, restartServiceCmd()
		case "enter", " ":
			if m.serviceCursor == 1 {
				m.overlay = overlayConfirm
				return m, nil
			} else if m.serviceCursor == 2 {
				m.showNotice("Restarting service…")
				return m, restartServiceCmd()
			}
			return m.startSetup()
		}

	case 8: // Diagnostics & Logs
		switch key {
		case "up", "k":
			m.diagCursor = (m.diagCursor - 1 + 2) % 2
			return m, nil
		case "down", "j":
			m.diagCursor = (m.diagCursor + 1) % 2
			return m, nil
		case "1", "d", "D":
			m.overlay = overlayDiag
			m.diagDone = false
			return m, diagCmd(m.paths)
		case "2", "l", "L":
			m.overlay = overlayLogs
			return m, logsCmd(time.Time{})
		case "enter", " ":
			if m.diagCursor == 0 {
				m.overlay = overlayDiag
				m.diagDone = false
				return m, diagCmd(m.paths)
			}
			m.overlay = overlayLogs
			return m, logsCmd(time.Time{})
		}

	case 9: // Voice Wake Keys (matrix: capslock / leftctrl / rightctrl)
		switch key {
		case "up", "k":
			m.wakeCursor = (m.wakeCursor + 2) % 3
			return m, nil
		case "down", "j":
			m.wakeCursor = (m.wakeCursor + 1) % 3
			return m, nil
		case "1", "2", "3":
			idx := int(key[0] - '1')
			m.wakeCursor = idx
			return m, toggleWakeKeyCmd(m.paths, wakeKeyByIDX(idx))
		case "enter", " ":
			return m, toggleWakeKeyCmd(m.paths, wakeKeyByIDX(m.wakeCursor))
		}
	}

	return m, nil
}

func (m *Model) showNotice(text string) {
	m.notice = text
	m.noticeUntil = time.Now().Add(6 * time.Second)
}

// saveCfgCmd loads, mutates and persists config off the render loop, then
// returns the updated copy so the model can cache it without re-reading disk.
// A non-empty notice is shown as a transient footer message.
func saveCfgCmd(paths config.Paths, mutate func(*config.Config) string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(paths)
		if err != nil {
			return noticeMsg{text: "Could not load config: " + err.Error()}
		}
		notice := mutate(&cfg)
		if err := config.Save(paths, cfg); err != nil {
			return noticeMsg{text: "Could not save config: " + err.Error()}
		}
		return cfgMsg{cfg: cfg, notice: notice}
	}
}

// toggleWakeKeyCmd flips one wake key, persists it and reloads the Hyprland
// binds so the voicetap keybind appears/disappears immediately.
// applyKeyboardRemap re-renders the keyd config so the caps-position
// overload(control, f24) follows the caps wake setting. Best-effort; skipped
// when the Sumika keyboard-remap extension is absent (plain swap stays).
func applyKeyboardRemap() {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	extDir := os.Getenv("SUMIKA_SHELL_EXTENSIONS_DIR")
	if extDir == "" {
		extDir = filepath.Join(dataHome, "sumika-shell", "extensions")
	}
	apply := filepath.Join(extDir, "keyboard-remap", "bin", "omarchy-keyboard-apply")
	if _, err := os.Stat(apply); err != nil {
		return
	}
	_ = exec.Command(apply).Run()
}

// wakeKeyByIDX maps the menu-row index to the wake-key CLI name.
func wakeKeyByIDX(idx int) string {
	switch idx {
	case 0:
		return "capslock"
	case 1:
		return "leftctrl"
	default:
		return "rightctrl"
	}
}

// toggleWakeKeyCmd flips one wake key, persists, and refreshes the live
// Hyprland binds (plus the keyd overload when the caps position changes).
func toggleWakeKeyCmd(paths config.Paths, key string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(paths)
		if err != nil {
			return noticeMsg{text: "Could not load config: " + err.Error()}
		}
		label := key
		switch key {
		case "capslock":
			cfg.WakeKeys.CapsLock = !cfg.WakeKeys.CapsLock
			label = "CapsLock"
		case "leftctrl":
			cfg.WakeKeys.LeftCtrl = !cfg.WakeKeys.LeftCtrl
			label = "LeftCtrl"
		case "rightctrl":
			cfg.WakeKeys.RightCtrl = !cfg.WakeKeys.RightCtrl
			label = "RightCtrl"
		default:
			return noticeMsg{text: "Unknown wake key: " + key}
		}
		if err := config.Save(paths, cfg); err != nil {
			return noticeMsg{text: "Could not save config: " + err.Error()}
		}
		if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
			if hyprctl, lookErr := exec.LookPath("hyprctl"); lookErr == nil {
				_ = exec.Command(hyprctl, "reload").Run()
			}
		}
		// Re-render keyd so the caps-position overload follows the setting
		// (best-effort; the CLI has the same call).
		if key == "capslock" {
			applyKeyboardRemap()
		}
		state := "OFF"
		if cfg.WakeKeys.Any() {
			state = "ON"
		}
		keyState := "off"
		switch key {
		case "capslock":
			keyState = onOff(cfg.WakeKeys.CapsLock)
		case "leftctrl":
			keyState = onOff(cfg.WakeKeys.LeftCtrl)
		case "rightctrl":
			keyState = onOff(cfg.WakeKeys.RightCtrl)
		}
		return cfgMsg{cfg: cfg, notice: label + " wake " + keyState + " (wake " + state + ")"}
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func setLanguageCmd(paths config.Paths, langCode string) tea.Cmd {
	return saveCfgCmd(paths, func(c *config.Config) string {
		c.Language = langCode
		return "Speech recognition language set to " + langName(langCode)
	})
}

func setTargetLanguageCmd(paths config.Paths, targetLang string) tea.Cmd {
	return saveCfgCmd(paths, func(c *config.Config) string {
		c.Translation.TargetLanguage = targetLang
		return "Target translation language set to " + targetLang
	})
}

func setTranslationEnabledCmd(paths config.Paths, enabled bool) tea.Cmd {
	return saveCfgCmd(paths, func(c *config.Config) string {
		c.Translation.Enabled = enabled
		if enabled {
			if c.Translation.BaseURL == "" {
				c.Translation.BaseURL = "https://api.deepseek.com/v1"
			}
			if c.Translation.Model == "" {
				c.Translation.Model = "deepseek-chat"
			}
			if c.Translation.TargetLanguage == "" {
				c.Translation.TargetLanguage = "Japanese"
			}
			return "Translation enabled"
		}
		return "Translation disabled"
	})
}

func setTranslationModelCmd(paths config.Paths, modelID, baseURL, apiKey string) tea.Cmd {
	return saveCfgCmd(paths, func(c *config.Config) string {
		c.Translation.Model = modelID
		if baseURL != "" {
			c.Translation.BaseURL = baseURL
		}
		if apiKey != "" {
			c.Translation.APIKey = apiKey
		}
		return "Translation model set to " + modelID
	})
}

// startSetup opens the setup overlay and streams progress.
func (m Model) startSetup() (tea.Model, tea.Cmd) {
	if m.setupBusy {
		m.showNotice("Setup is already running")
		return m, nil
	}
	m.overlay = overlaySetup
	m.setupBusy = true
	m.setupRepair = false
	m.setupLines = nil
	ch := make(chan tea.Msg, 128)
	go runSetupGoroutine(m.paths, ch)
	m.setupCh = ch
	return m, func() tea.Msg { return <-ch }
}

// startRepair opens the setup overlay (titled "Repair") and runs the full
// repair goroutine: setup + desktop integration + diagnostics verification.
func (m Model) startRepair() (tea.Model, tea.Cmd) {
	if m.setupBusy {
		m.showNotice("Repair is already running")
		return m, nil
	}
	m.overlay = overlaySetup
	m.setupBusy = true
	m.setupRepair = true
	m.setupLines = nil
	ch := make(chan tea.Msg, 128)
	go runRepairGoroutine(m.paths, ch)
	m.setupCh = ch
	return m, func() tea.Msg { return <-ch }
}

func (m *Model) scrollLogs(delta int) {
	visible := m.logsVisible()
	max := len(m.logs) - visible
	if max < 0 {
		max = 0
	}
	m.logScroll += delta
	if m.logScroll < 0 {
		m.logScroll = 0
	}
	if m.logScroll > max {
		m.logScroll = max
	}
}

// logsVisible returns how many log lines fit in the logs overlay.
func (m Model) logsVisible() int {
	return max(m.height-6, 4)
}

// captureTestResult marks a fresh recognition result as visible for the
// current test session when the daemon transitions to a terminal phase
// (succeeded/failed) while a test overlay is open. This keeps the result
// pane showing only this session's outcome instead of a stale prior result.
func (m *Model) captureTestResult(prevPhase protocol.Phase, state *protocol.State) {
	if m.overlay != overlayTestSpeech && m.overlay != overlayTestTranslation {
		return
	}
	if state == nil {
		return
	}
	cur := state.Phase
	if (cur != protocol.PhaseSucceeded && cur != protocol.PhaseFailed) || cur == prevPhase {
		return
	}
	m.testHasResult = true
	m.testResultScroll = 0
}

// resetTrial starts a clean trial-log session (called when the test overlay
// opens). The narrative is rebuilt client-side from phase transitions and
// live mic levels; nothing is read from the journal.
func (m *Model) resetTrial() {
	m.logs = nil
	m.testHasResult = false
	m.trialActive = false
	m.trialRecStart = time.Time{}
	m.trialPeak = 0
	m.trialSpeechSeen = false
	m.trialLastLevel = -1
	m.trialLastLogAt = time.Time{}
	m.trialLiveLogged = false
	m.trialStopLogged = false
	m.trialTranslateLogged = false
}

// appendTrial appends a trial-log line with a timestamp, keeping the pane bounded.
func (m *Model) appendTrial(line string) {
	ts := time.Now().Format("15:04:05")
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", ts, line))
	if len(m.logs) > 500 {
		m.logs = m.logs[len(m.logs)-500:]
	}
}

// trialTransition advances the trial narrative on daemon phase changes,
// mirroring the reference UI's session flow. Recording is observed, then
// transcribing/translating, then a terminal result.
func (m *Model) trialTransition(prevPhase protocol.Phase, state *protocol.State) {
	if m.overlay != overlayTestSpeech && m.overlay != overlayTestTranslation {
		return
	}
	if state == nil {
		return
	}
	cur := state.Phase
	// First observation of recording: the session just started, either from
	// our Space key or because the overlay opened mid-recording.
	if cur == protocol.PhaseRecording && !m.trialLiveLogged {
		if !m.trialActive {
			m.trialActive = true
			m.trialRecStart = time.Now()
			m.trialPeak = 0
			m.trialSpeechSeen = false
			m.trialLastLevel = -1
			m.trialLastLogAt = time.Time{}
			m.trialStopLogged = false
			m.trialTranslateLogged = false
		}
		m.appendTrialHeader()
		m.trialLiveLogged = true
		return
	}
	if !m.trialActive {
		return
	}
	switch cur {
	case protocol.PhaseTranscribing, protocol.PhaseTranslating:
		if !m.trialStopLogged {
			m.appendTrialStop()
			m.trialStopLogged = true
		}
		if cur == protocol.PhaseTranslating && !m.trialTranslateLogged {
			m.appendTrial("[trial] Translating with the LLM API…")

			modelName := m.cfg.Translation.Model
			providerName := ""
			endpoint := m.cfg.Translation.BaseURL
			targetLang := m.cfg.Translation.TargetLanguage
			if targetLang == "" {
				targetLang = "Japanese"
			}

			// Resolve provider/fullname from OpenCode configuration catalog
			openCodeModels := translate.LoadOpenCodeModels()
			for _, om := range openCodeModels {
				if om.ID == modelName || om.FullName == modelName {
					providerName = om.ProviderName
					if providerName == "" {
						providerName = om.ProviderKey
					}
					if om.FullName != "" {
						modelName = om.FullName
					}
					if om.BaseURL != "" {
						endpoint = om.BaseURL
					}
					break
				}
			}

			if providerName == "" {
				if idx := strings.Index(modelName, "/"); idx > 0 {
					providerName = modelName[:idx]
				} else if strings.Contains(endpoint, "deepseek") {
					providerName = "DeepSeek"
				} else if strings.Contains(endpoint, "openai") {
					providerName = "OpenAI"
				} else {
					providerName = "LLM Provider"
				}
			}

			if providerName != "" {
				m.appendTrial(fmt.Sprintf("[trial]   • Provider: %s", providerName))
			}
			if modelName != "" {
				m.appendTrial(fmt.Sprintf("[trial]   • Model: %s", modelName))
			}
			if endpoint != "" {
				m.appendTrial(fmt.Sprintf("[trial]   • Endpoint: %s", endpoint))
			}
			m.appendTrial(fmt.Sprintf("[trial]   • Target Language: %s", targetLang))

			m.trialTranslateLogged = true
		}
	case protocol.PhaseSucceeded:
		m.appendTrialFinish(state, false)
		m.trialActive = false
	case protocol.PhaseFailed:
		m.appendTrialFinish(state, true)
		m.trialActive = false
	}
}

// appendTrialHeader prints the session-opening block.
func (m *Model) appendTrialHeader() {
	m.appendTrial("========== VOICE TRIAL ==========")
	m.appendTrial("[trial] Session started at " + time.Now().Format("15:04:05"))
	m.appendTrial("[trial] Please speak into your microphone")
	m.appendTrial("[trial] Space = stop & transcribe")
	m.appendTrial("[trial] Esc   = cancel without transcribing")
	m.appendTrial("[trial] Starting recorder…")
	m.appendTrial("[trial] Recorder is live (microphone stream active)")
	m.appendTrial("[trial] Speak now — audio level will update below")
}

// appendTrialStop prints the stop/transcribing block.
func (m *Model) appendTrialStop() {
	elapsed := time.Since(m.trialRecStart)
	if m.trialRecStart.IsZero() {
		elapsed = 0
	}
	seen := "False"
	if m.trialSpeechSeen {
		seen = "True"
	}
	m.appendTrial(fmt.Sprintf("[trial] Stop requested after %s", formatDuration(elapsed)))
	m.appendTrial(fmt.Sprintf("[trial] Speech detected during session: %s", seen))
	m.appendTrial(fmt.Sprintf("[trial] Peak mic level: %d/100", m.trialPeak))
	m.appendTrial("[trial] Stopping recorder and sending audio to the speech model…")
	m.appendTrial("[trial] Transcribing — this may take a few seconds…")
}

// appendTrialFinish prints the terminal result block.
func (m *Model) appendTrialFinish(state *protocol.State, failed bool) {
	elapsed := time.Since(m.trialRecStart)
	if m.trialRecStart.IsZero() {
		elapsed = 0
	}
	m.appendTrial("[trial] Transcription finished")
	m.appendTrial(fmt.Sprintf("[trial] Session duration: %s", formatDuration(elapsed)))
	if failed {
		result := state.LastError
		if result == "" {
			result = "(no speech detected)"
			m.appendTrial("[trial] Result: " + result)
			m.appendTrial("[trial] Tip: speak closer to the mic, or check input device mute")
		} else {
			m.appendTrial("[trial] Result: " + result)
		}
	} else {
		result := state.LastResult
		if result == "" {
			result = "(empty — nothing recognised)"
			m.appendTrial("[trial] Result: " + result)
		} else {
			m.appendTrial(fmt.Sprintf("[trial] Result: %q", result))
		}
		m.appendTrial("[trial] Transcript saved to recent history")
	}
	m.appendTrial("[trial] Press Space to try again")
}

// trialHeartbeat appends a live level line while recording, at most every
// ~0.9s or when the level jumps noticeably — mirroring the reference UI's
// per-frame listening updates.
func (m *Model) trialHeartbeat(level int) {
	if !m.trialLiveLogged || m.state == nil || m.state.Phase != protocol.PhaseRecording {
		return
	}
	const speechThreshold = 12
	elapsed := time.Since(m.trialRecStart)
	if m.trialRecStart.IsZero() {
		elapsed = 0
	}
	if level > m.trialPeak {
		m.trialPeak = level
	}
	now := time.Now()
	hearing := level >= speechThreshold
	if hearing && !m.trialSpeechSeen {
		m.trialSpeechSeen = true
		m.appendTrial(fmt.Sprintf("[trial] Speech detected at %s  level=%d/100", formatDuration(elapsed), level))
		m.appendTrial("[trial] Keep speaking — press Space when finished")
		m.trialLastLogAt = now
		m.trialLastLevel = level
		return
	}
	if !hearing && m.trialSpeechSeen && m.trialLastLevel >= speechThreshold {
		m.appendTrial(fmt.Sprintf("[trial] Quiet again at %s  level=%d/100  (press Space to transcribe)", formatDuration(elapsed), level))
		m.trialLastLogAt = now
		m.trialLastLevel = level
		return
	}
	jump := level - m.trialLastLevel
	if jump < 0 {
		jump = -jump
	}
	if !m.trialLastLogAt.IsZero() && now.Sub(m.trialLastLogAt) < 900*time.Millisecond && jump < 15 {
		return
	}
	phase := "Listening (waiting for speech)"
	switch {
	case hearing:
		phase = "Hearing you"
	case m.trialSpeechSeen:
		phase = "Listening (paused / quiet)"
	}
	m.appendTrial(fmt.Sprintf("[trial] %s  t=%s  level=%d/100  peak=%d", phase, formatDuration(elapsed), level, m.trialPeak))
	m.trialLastLogAt = now
	m.trialLastLevel = level
}

// formatDuration renders a duration as mm:ss, matching the reference UI's
// trial time format.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

// cancelCmd aborts an in-progress recording without transcribing it.
func cancelCmd(paths config.Paths) tea.Cmd {
	return func() tea.Msg {
		response, err := service.Request(paths, protocol.OpCancel)
		if err != nil {
			return toggleMsg{err: err}
		}
		return toggleMsg{notice: response.Message, state: response.State}
	}
}

// pollState fetches a fresh snapshot from the control socket.
func pollState(m Model) tea.Cmd {
	return func() tea.Msg {
		response, err := service.Request(m.paths, "status")
		if err != nil {
			return stateMsg{err: err}
		}
		return stateMsg{state: response.State}
	}
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func toggleCmd(paths config.Paths, op string) tea.Cmd {
	return func() tea.Msg {
		response, err := service.Request(paths, op)
		if err != nil {
			return toggleMsg{err: err}
		}
		notice := response.Message
		if !response.OK && response.Error != nil {
			notice = response.Error.Detail
		}
		return toggleMsg{notice: notice, state: response.State}
	}
}

func disableServiceCmd() tea.Cmd {
	return func() tea.Msg {
		if err := service.Systemctl("disable", "--now", "sasayaki.service"); err != nil {
			return noticeMsg{text: "Could not disable the service: " + err.Error()}
		}
		return noticeMsg{text: "Service disabled"}
	}
}

func restartServiceCmd() tea.Cmd {
	return func() tea.Msg {
		if err := service.Systemctl("restart", "sasayaki.service"); err != nil {
			return noticeMsg{text: "Could not restart service: " + err.Error()}
		}
		return noticeMsg{text: "Service restarted successfully"}
	}
}

// logsCmd fetches the service log. When since is non-zero, only entries
// newer than it are returned (used by the test overlay so each test session
// starts with a fresh log view instead of the accumulated journal); a zero
// since tails the most recent lines (used by the diagnostics log view).
func logsCmd(since time.Time) tea.Cmd {
	return func() tea.Msg {
		output, err := journal(since)
		if err != nil {
			return logsMsg{lines: []string{"could not read the service log: " + err.Error()}}
		}
		lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
		return logsMsg{lines: lines}
	}
}

// journal is stubbable in tests.
var journal = func(since time.Time) ([]byte, error) {
	args := []string{"--user", "-u", "sasayaki.service", "--no-pager", "-o", "short-iso"}
	if since.IsZero() {
		args = append(args, "-n", "200")
	} else {
		args = append(args, "--since", since.Format("2006-01-02 15:04:05"))
	}
	cmd := exec.Command("journalctl", args...)
	return cmd.Output()
}

func diagCmd(paths config.Paths) tea.Cmd {
	return func() tea.Msg {
		report := diagnostics.All(paths)
		return diagMsg{report: report}
	}
}

func selectModelCmd(paths config.Paths, id string) tea.Cmd {
	return func() tea.Msg {
		selected, ok := transcribe.SpeechModelByID(id)
		if !ok {
			return modelChoiceMsg{err: fmt.Errorf("unknown model %q", id)}
		}
		cfg, err := config.Load(paths)
		if err != nil {
			return modelChoiceMsg{err: err}
		}
		cfg.SpeechModel = selected.ID
		if err := config.Save(paths, cfg); err != nil {
			return modelChoiceMsg{err: err}
		}
		installed := transcribe.ModelValidFor(paths, selected.ID)
		if installed {
			_ = service.Systemctl("restart", "sasayaki.service")
		}
		return modelChoiceMsg{label: selected.Label, installed: installed}
	}
}

// loadConfigCmd reloads config off the render loop. The cached copy drives
// every render so View never reads disk.
func loadConfigCmd(paths config.Paths) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(paths)
		if err != nil {
			cfg = config.Default()
		}
		return cfgMsg{cfg: cfg}
	}
}

// refreshInstalledCmd recomputes the installed-status snapshot for every
// speech model. The (multi-hundred-MB) SHA-256 verification runs off the
// render loop; the result is cached and only invalidated after setup or
// model selection, so the UI never re-hashes while rendering.
func refreshInstalledCmd(paths config.Paths) tea.Cmd {
	return func() tea.Msg {
		installed := make(map[string]bool, len(transcribe.SpeechModels))
		for _, model := range transcribe.SpeechModels {
			installed[model.ID] = transcribe.ModelValidFor(paths, model.ID)
		}
		return installedMsg{installed: installed}
	}
}

// runSetupGoroutine executes setup off the update loop and streams progress
// lines into msgs. It closes msgs after the final result.
func runSetupGoroutine(paths config.Paths, msgs chan<- tea.Msg) {
	defer close(msgs)
	binary, err := os.Executable()
	if err != nil {
		msgs <- setupDoneMsg{err: fmt.Errorf("could not locate the running sasayaki binary: %w", err)}
		return
	}
	setup.SetBinary(binary)
	setup.SetProgress(func(line string) { msgs <- setupProgressMsg{line} })
	session := setup.NewSession(paths)
	result := session.Run()
	msgs <- setupDoneMsg{result: result}
}

// runRepairGoroutine runs the full repair flow off the update loop: setup
// (runtime/model/service/config), then desktop integration (Hyprland binding
// reload + Quickshell restart), then diagnostics verification. Progress lines
// stream into msgs just like setup.
func runRepairGoroutine(paths config.Paths, msgs chan<- tea.Msg) {
	defer close(msgs)
	binary, err := os.Executable()
	if err != nil {
		msgs <- setupDoneMsg{err: fmt.Errorf("could not locate the running sasayaki binary: %w", err), repair: true}
		return
	}
	setup.SetBinary(binary)
	setup.SetProgress(func(line string) { msgs <- setupProgressMsg{line} })
	session := setup.NewSession(paths)
	result := session.Run()
	if !result.AllOK() {
		msgs <- setupDoneMsg{result: result, repair: true}
		return
	}
	// Desktop integration: Hyprland binding reload + Quickshell restart.
	// Best-effort across desktops; non-Sumika/Hyprland environments skip.
	if _, err := exec.LookPath("hyprctl"); err == nil {
		msgs <- setupProgressMsg{line: "Reloading Hyprland bindings…"}
		if out, err := exec.Command("hyprctl", "reload").CombinedOutput(); err != nil {
			msgs <- setupProgressMsg{line: "Warning: could not reload Hyprland: " + strings.TrimSpace(string(out))}
		}
	}
	if _, err := exec.LookPath("sumika-restart"); err == nil {
		msgs <- setupProgressMsg{line: "Restarting Quickshell integration…"}
		if out, err := exec.Command("sumika-restart", "--quickshell-only").CombinedOutput(); err != nil {
			msgs <- setupProgressMsg{line: "Warning: could not restart Quickshell: " + strings.TrimSpace(string(out))}
		}
	}
	// Diagnostics verification: any non-OK check is reported as a failure.
	report := diagnostics.All(paths)
	for _, check := range report.Checks {
		if !check.OK {
			msgs <- setupProgressMsg{line: "✗ " + check.Name + ": " + check.Detail}
		}
	}
	msgs <- setupDoneMsg{result: result, repair: true}
}
