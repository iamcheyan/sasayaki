package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// Rendering the model catalog must read install state from the cached
// snapshot, not re-hash the (multi-hundred-MB) ONNX files on every frame.
// The temp paths hold no model files, so an on-disk check would mark every
// model NOT DOWNLOADED; a cached "installed" entry must win instead.
func TestDetailCardUsesCachedInstalledSnapshot(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.menuCursor = 0
	m.activePanel = panelRight
	m.installed = map[string]bool{}
	for _, sm := range transcribe.SpeechModels {
		m.installed[sm.ID] = true
	}

	view := m.View()
	if strings.Contains(view, "[NOT DOWNLOADED]") {
		t.Fatalf("catalog must not fall back to an on-disk check when the cache says installed:\n%s", view)
	}
	if strings.Contains(view, "[···]") {
		t.Fatalf("catalog should not show a loading badge once the snapshot arrived:\n%s", view)
	}
}

// While the installed snapshot is still loading, the badge is neutral
// instead of hammering the filesystem.
func TestDetailCardInstalledLoading(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.menuCursor = 0

	view := m.View()
	if !strings.Contains(view, "[···]") {
		t.Fatalf("catalog should show a neutral loading badge before the snapshot arrives:\n%s", view)
	}
}

// The test overlay must render from cached config + state and never read
// disk. Driving it through the key path also exercises the 300ms-tick branch.
func TestTestSpeechOverlayRendersFromCache(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{
		Service:     protocol.ServiceRunning,
		Runtime:     true,
		Model:       true,
		SpeechModel: "sensevoice-int8",
		Language:    "zh",
		LastResult:  "hello",
	}

	// Press "t" to open the speech-test overlay, then a result arrives.
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	if m.overlay != overlayTestSpeech {
		t.Fatalf("expected test_speech overlay, got %q", m.overlay)
	}
	mm, _ = m.Update(stateMsg{state: &protocol.State{
		Service: protocol.ServiceRunning, Runtime: true, Model: true,
		SpeechModel: "sensevoice-int8", Language: "zh",
		Phase: protocol.PhaseSucceeded, LastResult: "hello",
	}})
	m = mm.(Model)
	view := m.View()
	for _, want := range []string{"Local Speech Recognition Test", "Recognition Result", "hello"} {
		if !strings.Contains(view, want) {
			t.Fatalf("test overlay missing %q:\n%s", want, view)
		}
	}
}

// Regression: the translation-test overlay must render and read its target
// language from the cached config, not from disk.
func TestTestTranslationOverlayRendersFromCache(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	cfg := config.Default()
	cfg.Translation.TargetLanguage = "Korean"
	m.cfg = cfg
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	m = mm.(Model)
	if m.overlay != overlayTestTranslation {
		t.Fatalf("expected test_translation overlay, got %q", m.overlay)
	}
	view := m.View()
	if !strings.Contains(view, "Speech Translation Test") {
		t.Fatalf("missing title:\n%s", view)
	}
	if !strings.Contains(view, "Korean") {
		t.Fatalf("target language should come from cached config:\n%s", view)
	}
}

// Long transcripts must render complete (never truncated to a summary) and
// wrap onto several lines instead of overflowing the popup.
func TestTestSpeechOverlayShowsFullTranscript(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	long := strings.Repeat("很长的中文语音文本，", 150) // ~1500 cols: must overflow the result pane
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, LastResult: long}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	// A result arrives during the session: phase transitions to succeeded.
	mm, _ = m.Update(stateMsg{state: &protocol.State{
		Service: protocol.ServiceRunning, Runtime: true, Model: true,
		Phase: protocol.PhaseSucceeded, LastResult: long,
	}})
	m = mm.(Model)
	view := m.View()
	// The transcript wraps across many lines: the start is visible at the
	// top and the tail is reachable by scrolling the result pane.
	if !strings.Contains(view, long[:40]) {
		t.Fatalf("test overlay must start the complete transcript:\n%s", view)
	}
	if !strings.Contains(view, "↑/↓ scroll") {
		t.Fatalf("long transcripts should expose scrolling:\n%s", view)
	}
	if !strings.Contains(view, "Tab switch") {
		t.Fatalf("an overflowing result pane should hint at Tab to switch panes:\n%s", view)
	}
	// The logs pane is active by default: j must not scroll the result.
	for i := 0; i < 20; i++ {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = mm.(Model)
	}
	if view := m.View(); !strings.Contains(view, long[:40]) {
		t.Fatalf("scrolling the logs pane must not move the recognition pane")
	}
	// Tab selects the result pane; scrolling it reveals the transcript tail.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = mm.(Model)
	for i := 0; i < 200; i++ {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = mm.(Model)
	}
	if view := m.View(); !strings.Contains(view, long[len(long)-20:]) {
		t.Fatalf("scrolling the result pane must reveal the transcript tail:\n%s", view)
	}
}

// The logs pane scrolls independently: scrolling it never moves the pinned
// recognition pane, and the footer offers a way back to follow mode.
func TestTestSpeechLogsScrollKeepsResultPinned(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, LastResult: "hi"}

	// Opening the overlay clears the log buffer; logs then stream in via
	// logsMsg, simulated here by populating m.logs after the open.
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	for i := 0; i < 60; i++ {
		m.logs = append(m.logs, fmt.Sprintf("2026-08-02T13:23:00+09:00 sasayaki[%d] level=INFO msg=line%02d", 1000+i, i))
	}
	top := m.View()
	if !strings.Contains(top, "Recognition Result:") {
		t.Fatalf("recognition pane should be pinned at the top:\n%s", top)
	}
	if !strings.Contains(top, "line59") || strings.Contains(top, "line00") {
		t.Fatalf("logs should auto-follow the newest line (line59 visible, line00 not):\n%s", top)
	}

	// Scroll the logs pane up twice; the recognition pane must stay put.
	for i := 0; i < 2; i++ {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		m = mm.(Model)
	}
	scrolled := m.View()
	if !strings.Contains(scrolled, "Recognition Result:") {
		t.Fatalf("scrolling logs must not move the recognition pane:\n%s", scrolled)
	}
	if strings.Contains(scrolled, "line59") {
		t.Fatalf("scrolling up should leave the newest line behind:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "↓ follow") {
		t.Fatalf("footer should hint how to resume following:\n%s", scrolled)
	}
}

// The logs pane follows new lines while in follow mode, stays put after the
// user scrolls away, and G restores following.
func TestTestSpeechLogsAutoFollow(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	for i := 0; i < 40; i++ {
		m.logs = append(m.logs, fmt.Sprintf("log line %02d ...", i))
	}
	view := m.View()
	if !strings.Contains(view, "log line 39") || strings.Contains(view, "log line 00") {
		t.Fatalf("follow mode should pin the newest lines (39 visible, 00 not):\n%s", view)
	}

	// New logs arrive; still following, so the newest appears.
	m.logs = append(m.logs, "log line 40 ...")
	view = m.View()
	if !strings.Contains(view, "log line 40") {
		t.Fatalf("following should track the newest line:\n%s", view)
	}

	// Scroll up; follow off. New logs no longer move the viewport.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = mm.(Model)
	m.logs = append(m.logs, "log line 41 ...")
	view = m.View()
	if strings.Contains(view, "log line 41") || strings.Contains(view, "log line 40") {
		t.Fatalf("after scrolling away, new logs must not move the viewport:\n%s", view)
	}
	if !strings.Contains(view, "log line 39") {
		t.Fatalf("the visible tail should stay put after scrolling away:\n%s", view)
	}

	// G resumes following; the newest line appears.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = mm.(Model)
	view = m.View()
	if !strings.Contains(view, "log line 41") {
		t.Fatalf("G should resume following the newest line:\n%s", view)
	}
}

// The translation overlay must keep the original transcript in Recognition
// Output and the translated text in Translation Output — they are different
// texts now, not both the same LastResult.
func TestTestTranslationOverlayKeepsTranscriptSeparate(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{
		Service:    protocol.ServiceRunning,
		Runtime:    true,
		Model:      true,
		Transcript: "原始中文语音文本",
		LastResult: "translated english text",
	}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	m = mm.(Model)
	// Results arrive during the session.
	mm, _ = m.Update(stateMsg{state: &protocol.State{
		Service: protocol.ServiceRunning, Runtime: true, Model: true,
		Phase:      protocol.PhaseSucceeded,
		Transcript: "原始中文语音文本", LastResult: "translated english text",
	}})
	m = mm.(Model)
	view := m.View()
	if !strings.Contains(view, "原始中文语音文本") {
		t.Fatalf("Recognition Output must show the original transcript:\n%s", view)
	}
	if !strings.Contains(view, "translated english text") {
		t.Fatalf("Translation Output must show the translated text:\n%s", view)
	}
}

// wrapWide must break at word boundaries and never emit a line wider than
// the requested width, while still wrapping CJK text (no spaces) by column.
func TestWrapWideWordAndCJK(t *testing.T) {
	words := strings.Repeat("word ", 40)
	for _, line := range wrapWide(words, 30) {
		if lipgloss.Width(line) > 30 {
			t.Fatalf("line %q wider than 30", line)
		}
	}
	cjk := strings.Repeat("中文", 30)
	for _, line := range wrapWide(cjk, 20) {
		if lipgloss.Width(line) > 20 {
			t.Fatalf("CJK line %q wider than 20", line)
		}
	}
	if got := wrapWide(words, 30); len(got) < 2 {
		t.Fatalf("word wrapping should produce multiple lines, got %d", len(got))
	}
}

// The test overlay surfaces a model/runtime status row so the user can see
// whether the ONNX model is loaded in RAM — the "how it works" cue.
func TestTestSpeechOverlayShowsModelStatus(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{
		Service:     protocol.ServiceRunning,
		Runtime:     true,
		Model:       true,
		SpeechModel: "sensevoice-int8",
		Worker:      transcribe.WorkerWarm,
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	view := m.View()
	if !strings.Contains(view, "Loaded in RAM") {
		t.Fatalf("warm model should report Loaded in RAM:\n%s", view)
	}
	if !strings.Contains(view, "SenseVoice Small") {
		t.Fatalf("model status should name the model:\n%s", view)
	}

	// A dead worker must read as stopped, not loaded.
	m.state.Worker = transcribe.WorkerDead
	if view := m.View(); !strings.Contains(view, "Engine stopped") {
		t.Fatalf("dead worker should report Engine stopped:\n%s", view)
	}
}

// Opening the test overlay must NOT show a stale result from a prior
// session: the result pane starts empty until a fresh recognition completes
// during this session.
func TestTestSpeechOverlayHidesStaleResultOnOpen(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{
		Service:     protocol.ServiceRunning,
		Runtime:     true,
		Model:       true,
		SpeechModel: "sensevoice-int8",
		Worker:      transcribe.WorkerWarm,
		Phase:       protocol.PhaseSucceeded,
		LastResult:  "stale result from last time",
	}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	if view := m.View(); strings.Contains(view, "stale result from last time") {
		t.Fatalf("opening the overlay must hide the stale result:\n%s", view)
	}
	if view := m.View(); !strings.Contains(view, "Press Space to record audio") {
		t.Fatalf("result pane should show the ready placeholder until a fresh result arrives:\n%s", view)
	}

	// A fresh recognition runs during the session: recording → succeeded.
	mm, _ = m.Update(stateMsg{state: &protocol.State{
		Service: protocol.ServiceRunning, Runtime: true, Model: true,
		Phase: protocol.PhaseRecording,
	}})
	m = mm.(Model)
	mm, _ = m.Update(stateMsg{state: &protocol.State{
		Service: protocol.ServiceRunning, Runtime: true, Model: true,
		Phase: protocol.PhaseSucceeded, LastResult: "fresh result",
	}})
	m = mm.(Model)
	if view := m.View(); !strings.Contains(view, "fresh result") {
		t.Fatalf("a fresh session result must be shown:\n%s", view)
	}
}

// Opening the test overlay resets the trial-log buffer and session state,
// so each test session starts with a fresh narrative instead of a stale one.
func TestTestSpeechOverlayClearsLogsOnOpen(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true}
	m.logs = []string{"stale log line 1", "stale log line 2"}
	m.trialActive = true
	m.trialRecStart = time.Now().Add(-time.Minute)

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	if len(m.logs) != 0 {
		t.Fatalf("opening the overlay should clear stale logs, got %v", m.logs)
	}
	if m.trialActive {
		t.Fatal("opening the overlay should reset the trial session")
	}
	if !m.trialRecStart.IsZero() {
		t.Fatal("opening the overlay should clear the trial start time")
	}
}

// The trial log is a client-side narrative rebuilt from phase transitions and
// live mic levels (no journalctl): header on recording, heartbeats while
// listening, stop block on transcribing, result block on success.
func TestTrialLogNarrative(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseIdle}

	// Open the speech test overlay.
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)

	// Space starts a session.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = mm.(Model)
	if !m.trialActive {
		t.Fatal("Space should start a trial session")
	}
	m.trialRecStart = time.Now().Add(-5 * time.Second) // fixed elapsed for asserts

	// Daemon reports recording: header block.
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseRecording}})
	m = mm.(Model)
	joined := strings.Join(m.logs, "\n")
	for _, want := range []string{
		"========== VOICE TRIAL ==========", "Session started at",
		"Please speak into your microphone", "Space = stop & transcribe",
		"Esc   = cancel without transcribing", "Starting recorder",
		"Recorder is live", "Speak now — audio level will update below",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("trial header missing %q:\n%s", want, joined)
		}
	}

	// Quiet tick: listening heartbeat.
	mm, _ = m.Update(tickMsg{})
	m = mm.(Model)
	joined = strings.Join(m.logs, "\n")
	if !strings.Contains(joined, "Listening (waiting for speech)  t=00:05") {
		t.Fatalf("expected listening heartbeat:\n%s", joined)
	}

	// Speech crosses the threshold: detection notice, then hearing heartbeat.
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseRecording, MicLevel: 40}})
	m = mm.(Model)
	mm, _ = m.Update(tickMsg{})
	m = mm.(Model)
	joined = strings.Join(m.logs, "\n")
	if !strings.Contains(joined, "Speech detected at 00:05  level=40/100") {
		t.Fatalf("expected speech detection line:\n%s", joined)
	}
	// Force the next heartbeat past the ~0.9s throttle.
	m.trialLastLogAt = time.Time{}
	mm, _ = m.Update(tickMsg{})
	m = mm.(Model)
	joined = strings.Join(m.logs, "\n")
	if !strings.Contains(joined, "Hearing you  t=00:05  level=40/100  peak=40") {
		t.Fatalf("expected hearing heartbeat:\n%s", joined)
	}

	// Stop: daemon transcribes.
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseTranscribing}})
	m = mm.(Model)
	joined = strings.Join(m.logs, "\n")
	for _, want := range []string{
		"Stop requested after 00:05", "Speech detected during session: True",
		"Peak mic level: 40/100", "Transcribing — this may take a few seconds",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stop block missing %q:\n%s", want, joined)
		}
	}

	// Success: result block, session ends.
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseSucceeded, LastResult: "hello world"}})
	m = mm.(Model)
	joined = strings.Join(m.logs, "\n")
	for _, want := range []string{
		"Transcription finished", "Session duration: 00:05",
		`Result: "hello world"`, "Transcript saved to recent history",
		"Press Space to try again",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("result block missing %q:\n%s", want, joined)
		}
	}
	if m.trialActive {
		t.Fatal("session should end after a terminal result")
	}
}

// Esc while recording cancels without transcribing and stays in the overlay;
// q still exits.
func TestTrialEscCancelsRecording(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	m.cfg = config.Default()
	m.state = &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseIdle}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = mm.(Model)
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseRecording}})
	m = mm.(Model)

	// Esc cancels and stays.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.overlay != overlayTestSpeech {
		t.Fatalf("Esc while recording should stay in the overlay, got %q", m.overlay)
	}
	joined := strings.Join(m.logs, "\n")
	if !strings.Contains(joined, "Recording cancelled — audio discarded (not transcribed)") {
		t.Fatalf("expected cancel line:\n%s", joined)
	}
	if m.trialActive {
		t.Fatal("cancel should end the trial session")
	}

	// The daemon reports the cancelled (idle) state; Esc now exits.
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseIdle}})
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if m.overlay != overlayNone {
		t.Fatalf("Esc while idle should exit the overlay, got %q", m.overlay)
	}
}

func TestTrialTranslationLogsModelDetails(t *testing.T) {
	m := New(testPaths(t))
	m.width, m.height = 120, 40
	m.layout = computeLayout(m.width, m.height)
	cfg := config.Default()
	cfg.Translation.Enabled = true
	cfg.Translation.Model = "opencode-go/deepseek-v4-pro"
	cfg.Translation.BaseURL = "https://opencode.ai/zen/go/v1"
	cfg.Translation.TargetLanguage = "Japanese"
	m.cfg = cfg

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	m = mm.(Model)

	// Simulate recording then translating phase
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseRecording}})
	m = mm.(Model)
	mm, _ = m.Update(stateMsg{state: &protocol.State{Service: protocol.ServiceRunning, Runtime: true, Model: true, Phase: protocol.PhaseTranslating}})
	m = mm.(Model)

	joined := strings.Join(m.logs, "\n")
	for _, want := range []string{
		"Translating with the LLM API…",
		"Provider:",
		"Model: opencode-go/deepseek-v4-pro",
		"Endpoint: https://opencode.ai/zen/go/v1",
		"Target Language: Japanese",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("translation trial logs missing %q:\n%s", want, joined)
		}
	}
}
