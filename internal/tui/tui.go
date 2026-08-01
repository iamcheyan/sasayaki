// Package tui is the Sasayaki control center: two equal cards (VOICE /
// RUNTIME) with spatial arrow-key navigation, letter shortcuts, overlays
// and transient notices. It is a thin client over the control socket and
// never talks to the model or the microphone itself.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/service"
	"github.com/iamcheyan/sasayaki/internal/setup"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// overlays
const (
	overlayNone    = ""
	overlayHelp    = "help"
	overlayKeys    = "shortcut"
	overlayLogs    = "logs"
	overlayDiag    = "diagnose"
	overlaySetup   = "setup"
	overlayConfirm = "confirm"
	overlayModels  = "models"
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
	label string
	err   error
}

type setupProgressMsg struct{ line string }

type setupDoneMsg struct {
	result setup.PlanResult
	err    error
}

type logsMsg struct{ lines []string }

type diagMsg struct {
	report diagnostics.Report
}

type tickMsg struct{}

// Model is the TUI state.
type Model struct {
	paths config.Paths
	theme theme

	width  int
	height int
	layout layout
	focus  int

	state    *protocol.State
	gotState bool

	notice      string
	noticeUntil time.Time

	overlay string

	logs      []string
	logScroll int
	diag      diagnostics.Report
	diagDone  bool

	setupLines []string
	setupCh    <-chan tea.Msg
	setupBusy  bool

	confirmTarget string
}

// New builds the TUI model.
func New(paths config.Paths) Model {
	return Model{
		paths: paths,
		theme: defaultTheme(),
		focus: focusRecord,
	}
}

// Run starts the TUI and blocks until quit.
func Run(paths config.Paths) error {
	program := tea.NewProgram(New(paths), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

// Init starts state polling and the notice clock.
func (m Model) Init() tea.Cmd {
	return tea.Batch(pollState(m), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout = computeLayout(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		// Poll more eagerly while an operation is in flight.
		interval := 2 * time.Second
		if m.state != nil {
			switch m.state.Phase {
			case protocol.PhaseRecording, protocol.PhaseTranscribing, protocol.PhaseTranslating, protocol.PhasePasting:
				interval = 500 * time.Millisecond
			}
		}
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
		}
		return m, tea.Batch(pollState(m), tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} }))

	case stateMsg:
		if msg.err != nil {
			// Socket vanished: clear the snapshot so the UI shows STOPPED.
			m.state = nil
			m.gotState = false
			return m, nil
		}
		m.state = msg.state
		m.gotState = true
		return m, nil

	case toggleMsg:
		if msg.err != nil {
			m.showNotice(msg.err.Error())
			return m, nil
		}
		if msg.state != nil {
			m.state = msg.state
			m.gotState = true
		}
		if msg.notice != "" {
			m.showNotice(msg.notice)
		}
		return m, nil

	case noticeMsg:
		m.showNotice(msg.text)
		return m, nil

	case modelChoiceMsg:
		if msg.err != nil {
			m.showNotice("Could not select model: " + msg.err.Error())
		} else {
			m.showNotice("Selected " + msg.label + " — press S to download/apply it")
		}
		return m, nil

	case setupProgressMsg:
		m.setupLines = append(m.setupLines, msg.line)
		// Re-arm the stream for the next progress line.
		return m, func() tea.Msg { return <-m.setupCh }

	case setupDoneMsg:
		m.setupBusy = false
		m.setupCh = nil
		m.setupLines = append(m.setupLines, summaryLine(msg))
		if msg.err != nil {
			m.setupLines = append(m.setupLines, "setup failed: "+msg.err.Error())
		}
		m.showNotice("setup " + outcomeWord(msg))
		return m, nil

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
		return "setup stopped: " + msg.err.Error()
	}
	if msg.result.AllOK() {
		skipped := fmt.Sprintf("%d skipped", msg.result.Skipped)
		return "setup complete — " + skipped
	}
	return "setup incomplete: " + strings.Join(msg.result.Failed, ", ")
}

func outcomeWord(msg setupDoneMsg) string {
	if msg.err != nil {
		return "failed"
	}
	if msg.result.AllOK() {
		return "complete"
	}
	return "incomplete"
}

// handleKey implements the full key contract: letters for significant
// actions, arrows/Tab for spatial focus, Enter for the focused action,
// Esc to close overlays, Q to quit.
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

	if m.overlay != overlayNone {
		if m.overlay == overlayModels {
			switch key {
			case "1":
				return m, selectModelCmd(m.paths, "sensevoice-int8")
			case "2":
				return m, selectModelCmd(m.paths, "sensevoice-full")
			case "esc", "m", "M":
				m.overlay = overlayNone
			}
			return m, nil
		}
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

	switch key {
	case "q", "Q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m, nil
	case "t", "T":
		return m, toggleCmd(m.paths)
	case "s", "S":
		return m.startSetup()
	case "r", "R":
		return m.startSetup()
	case "d", "D":
		m.overlay = overlayConfirm
		return m, nil
	case "b", "B":
		m.overlay = overlayKeys
		return m, nil
	case "m", "M":
		m.overlay = overlayModels
		return m, nil
	case "L":
		m.overlay = overlayLogs
		return m, logsCmd()
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "left", "h":
		m.focus = moveFocus("left", m.focus)
	case "right", "l":
		m.focus = moveFocus("right", m.focus)
	case "up", "k":
		m.focus = moveFocus("up", m.focus)
	case "down", "j":
		m.focus = moveFocus("down", m.focus)
	case "tab":
		m.focus = moveFocus("tab", m.focus)
	case "enter", " ":
		return m.activateFocused()
	}
	return m, nil
}

func (m *Model) showNotice(text string) {
	m.notice = text
	m.noticeUntil = time.Now().Add(6 * time.Second)
}

// activateFocused runs the harmless action under the focus.
func (m Model) activateFocused() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusRecord:
		return m, toggleCmd(m.paths)
	case focusShortcut:
		m.overlay = overlayKeys
		return m, nil
	case focusSetup:
		return m.startSetup()
	case focusDiagnose:
		m.overlay = overlayDiag
		m.diagDone = false
		return m, diagCmd(m.paths)
	case focusLogs:
		m.overlay = overlayLogs
		return m, logsCmd()
	}
	return m, nil
}

// startSetup opens the setup overlay and streams progress.
func (m Model) startSetup() (tea.Model, tea.Cmd) {
	if m.setupBusy {
		m.showNotice("Setup is already running")
		return m, nil
	}
	m.overlay = overlaySetup
	m.setupBusy = true
	m.setupLines = nil
	ch := make(chan tea.Msg, 128)
	go runSetupGoroutine(m.paths, ch)
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
	return maxInt(m.height-6, 4)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func toggleCmd(paths config.Paths) tea.Cmd {
	return func() tea.Msg {
		response, err := service.Request(paths, "toggle")
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

func logsCmd() tea.Cmd {
	return func() tea.Msg {
		output, err := journal()
		if err != nil {
			return logsMsg{lines: []string{"could not read the service log: " + err.Error()}}
		}
		lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
		return logsMsg{lines: lines}
	}
}

// journal is stubbable in tests.
var journal = func() ([]byte, error) {
	cmd := exec.Command("journalctl", "--user", "-u", "sasayaki.service", "-n", "200", "--no-pager", "-o", "short-iso")
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
		return modelChoiceMsg{label: selected.Label}
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
