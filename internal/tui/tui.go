package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/service"
	"github.com/iamcheyan/sasayaki/internal/setup"
)

var (
	violet = lipgloss.Color("141")
	mint   = lipgloss.Color("79")
	muted  = lipgloss.Color("245")
	amber  = lipgloss.Color("221")
	title  = lipgloss.NewStyle().Foreground(violet).Bold(true)
	dim    = lipgloss.NewStyle().Foreground(muted)
	ok     = lipgloss.NewStyle().Foreground(mint).Bold(true)
	alert  = lipgloss.NewStyle().Foreground(amber).Bold(true)
	focus  = lipgloss.NewStyle().Foreground(violet).Bold(true)
)

type Model struct {
	paths         config.Paths
	width, height int
	focus         int
	state         *protocol.State
	notice        string
	overlay       string
}

type statusMsg struct {
	state *protocol.State
	err   error
}
type noticeMsg string

func New(paths config.Paths) Model { return Model{paths: paths} }
func (m Model) Init() tea.Cmd      { return refresh(m.paths) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case statusMsg:
		if msg.err != nil {
			m.state = &protocol.State{Service: "stopped"}
			m.notice = "Service is not running — press S to set it up"
		} else {
			m.state = msg.state
		}
	case noticeMsg:
		m.notice = string(msg)
		return m, clearNotice()
	case tea.KeyMsg:
		if m.overlay != "" {
			if msg.String() == "esc" || msg.String() == "?" {
				m.overlay = ""
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.overlay = "help"
		case "b":
			m.overlay = "shortcut"
		case "up", "left":
			if m.focus > 0 {
				m.focus--
			}
		case "down", "right", "tab":
			if m.focus < 3 {
				m.focus++
			} else {
				m.focus = 0
			}
		case "t":
			return m, action(m.paths, "toggle")
		case "s":
			return m, setupAction(m.paths)
		case "d":
			return m, serviceAction("disable", "--now", "sasayaki.service")
		case "enter":
			switch m.focus {
			case 0:
				return m, action(m.paths, "toggle")
			case 1:
				return m, setupAction(m.paths)
			case 2:
				m.overlay = "shortcut"
			case 3:
				return m, serviceAction("restart", "sasayaki.service")
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	maxWidth := min(m.width-4, 118)
	if maxWidth < 40 {
		maxWidth = m.width
	}
	head := title.Render("✦  sasayaki") + "  " + dim.Render("local voice input")
	status := dim.Render("○ SETUP NEEDED")
	if m.state != nil && m.state.Service == "running" {
		status = ok.Render("● READY")
	}
	head = lipgloss.JoinHorizontal(lipgloss.Top, head, strings.Repeat(" ", max(1, maxWidth-lipgloss.Width(head)-lipgloss.Width(status))), status)
	subtitle := dim.Render("Speak naturally. Sasayaki transcribes locally and pastes where you are typing.")
	left := m.card("VOICE", []string{
		m.row(0, "◆ RECORDING", recordingText(m.state)),
		"", m.row(2, "◆ SHORTCUT", "Bind: sasayaki toggle"),
		"", dim.Render("Toggle once to record, again to transcribe."),
	}, 0)
	right := m.card("RUNTIME", []string{
		m.row(1, "◆ LOCAL ENGINE", readiness(m.state)),
		"", m.row(3, "◆ SERVICE", serviceText(m.state)),
		"", dim.Render("Model, private Python runtime and user service."),
	}, 2)
	content := ""
	if maxWidth >= 78 {
		content = lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	} else {
		content = left + "\n\n" + right
	}
	footer := focus.Render("[T]") + dim.Render(" toggle  ") + focus.Render("[S]") + dim.Render(" setup  ") + focus.Render("[B]") + dim.Render(" shortcut  ") + focus.Render("[?]") + dim.Render(" help  ") + focus.Render("[Q]") + dim.Render(" quit")
	if m.notice != "" {
		footer = alert.Render(m.notice)
	}
	view := head + "\n" + subtitle + "\n\n" + content + "\n\n" + lipgloss.PlaceHorizontal(maxWidth, lipgloss.Center, footer)
	view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, view)
	if m.overlay != "" {
		return overlay(m.width, m.height, m.overlay)
	}
	return view
}

func (m Model) card(name string, lines []string, focusIndex int) string {
	width := 56
	if m.width < 78 {
		width = max(34, min(m.width-4, 64))
	} else {
		width = (min(m.width-4, 118) - 2) / 2
	}
	body := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(violet).Width(width).Height(12).Padding(0, 1).Render(title.Render(name) + "\n" + body)
}

func (m Model) row(index int, label, value string) string {
	labelStyle := dim
	if index == m.focus {
		labelStyle = focus
	}
	return labelStyle.Render(label) + "\n" + "  " + value
}

func recordingText(s *protocol.State) string {
	if s != nil && s.Recording {
		return alert.Render("● Listening — press toggle to finish")
	}
	return ok.Render("● Ready to listen")
}
func serviceText(s *protocol.State) string {
	if s != nil && s.Service == "running" {
		return ok.Render("● Running")
	}
	return alert.Render("○ Not running")
}
func readiness(s *protocol.State) string {
	if s != nil && s.Model && s.Runtime {
		return ok.Render("● SenseVoice installed")
	}
	return alert.Render("○ Setup required")
}

func overlay(width, height int, name string) string {
	var text string
	if name == "shortcut" {
		text = title.Render("Global shortcut") + "\n\n" + "Bind this command in your desktop's keyboard settings:\n\n" + focus.Render("  sasayaki toggle") + "\n\n" + dim.Render("KDE: System Settings → Shortcuts\nGNOME: Settings → Keyboard → Custom Shortcuts\nHyprland/Sway: bind the command in your compositor config.\n\nEsc closes this guide.")
	} else {
		text = title.Render("Sasayaki help") + "\n\n" + "T  start or finish recording\nS  install or repair local runtime and service\nB  show global shortcut instructions\nD  stop and disable the user service\nArrows / Tab  move focus\nQ  quit\n\n" + dim.Render("All speech remains local. Esc closes this help.")
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(violet).Background(lipgloss.Color("235")).Padding(1, 2).Width(min(width-8, 76)).Render(text)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func refresh(p config.Paths) tea.Cmd {
	return func() tea.Msg { r, err := service.Request(p, "status"); return statusMsg{state: r.State, err: err} }
}
func action(p config.Paths, operation string) tea.Cmd {
	return func() tea.Msg {
		r, err := service.Request(p, operation)
		if err != nil {
			return noticeMsg(err.Error())
		}
		return noticeMsg(r.Message)
	}
}
func setupAction(p config.Paths) tea.Cmd {
	return func() tea.Msg {
		binary, err := exec.LookPath("sasayaki")
		if err != nil {
			return noticeMsg("Run setup from the installed sasayaki binary")
		}
		err = setup.Run(p, binary, func(string) {})
		if err != nil {
			return noticeMsg("Setup failed: " + err.Error())
		}
		return noticeMsg("Setup complete — Sasayaki is ready")
	}
}
func serviceAction(args ...string) tea.Cmd {
	return func() tea.Msg {
		if err := service.Systemctl(args...); err != nil {
			return noticeMsg(err.Error())
		}
		return noticeMsg("Service updated")
	}
}
func clearNotice() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return noticeMsg("") })
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Run(paths config.Paths) error {
	_, err := tea.NewProgram(New(paths), tea.WithAltScreen()).Run()
	return err
}

var _ = fmt.Sprint
