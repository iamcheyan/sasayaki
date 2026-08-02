package paste

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every command and lets tests control LookPath results.
type fakeRunner struct {
	present   map[string]bool
	runs      []cmdCall
	stdin     []string
	failRun   map[string]bool
	failStdin map[string]bool
}

type cmdCall struct {
	name string
	args []string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.present[name] {
		return "/usr/bin/" + name, nil
	}
	return "", &lookPathError{name}
}

type lookPathError struct{ name string }

func (e *lookPathError) Error() string { return "not found: " + e.name }

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.runs = append(f.runs, cmdCall{name, args})
	if f.failRun[name] {
		return []byte("paste failed"), &runError{name}
	}
	return nil, nil
}

func (f *fakeRunner) RunStdin(name string, args []string, stdin []byte) ([]byte, error) {
	f.runs = append(f.runs, cmdCall{name, args})
	f.stdin = append(f.stdin, string(stdin))
	if f.failStdin[name] {
		return []byte("copy failed"), &runError{name}
	}
	return nil, nil
}

type runError struct{ name string }

func (e *runError) Error() string { return e.name + " exited nonzero" }

func fullRunner() *fakeRunner {
	return &fakeRunner{present: map[string]bool{
		"wl-copy": true, "wtype": true,
	}}
}

func TestPasteExactArgvWtype(t *testing.T) {
	r := fullRunner()
	result := PasteWith(r, "hello")
	if !result.Pasted {
		t.Fatalf("PasteWith = %+v, want pasted", result)
	}
	if result.Backend != "wtype" {
		t.Fatalf("backend = %q, want wtype", result.Backend)
	}
	// Clipboard first: wl-copy with --trim-newline and the exact text.
	// Then the wtype Ctrl+Shift+V chord.
	want := []cmdCall{
		{"wl-copy", []string{"--trim-newline"}},
		{"wtype", []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"}},
	}
	if !reflect.DeepEqual(r.runs, want) {
		t.Fatalf("calls = %+v, want %+v", r.runs, want)
	}
	if len(r.stdin) != 1 || r.stdin[0] != "hello" {
		t.Fatalf("clipboard stdin = %q, want %q", r.stdin, "hello")
	}
}

func TestPasteFallbackOrder(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{
		"wl-copy": true, "wtype": true, "ydotool": true, "xdotool": true,
	}}
	r.failRun = map[string]bool{"wtype": true, "ydotool": true}
	result := PasteWith(r, "text")
	if !result.Pasted {
		t.Fatal("expected xdotool fallback to succeed")
	}
	if result.Backend != "xdotool" {
		t.Fatalf("backend = %q, want xdotool", result.Backend)
	}
	// The three chord attempts in order.
	var chords []string
	for _, call := range r.runs {
		if call.name != "wl-copy" {
			chords = append(chords, call.name)
		}
	}
	if !reflect.DeepEqual(chords, []string{"wtype", "ydotool", "xdotool"}) {
		t.Fatalf("chord order = %v", chords)
	}
	if !reflect.DeepEqual(r.runs[len(r.runs)-1].args, []string{"key", "--clearmodifiers", "ctrl+v"}) {
		t.Fatalf("xdotool args = %v", r.runs[len(r.runs)-1].args)
	}
}

func TestPasteTruthfulClipboardFallback(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"wl-copy": true}}
	// No paste backend at all.
	result := PasteWith(r, "text")
	if result.Pasted {
		t.Fatal("PasteWith must not claim a paste without a backend")
	}
	if result.Backend != "clipboard" {
		t.Fatalf("backend = %q, want clipboard", result.Backend)
	}
	if !strings.Contains(result.Detail, "paste it manually") {
		t.Fatalf("detail should tell the user to paste manually: %q", result.Detail)
	}
}

func TestPasteNoClipboard(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{}}
	result := PasteWith(r, "text")
	if result.Pasted {
		t.Fatal("must not claim success without a clipboard")
	}
	if !strings.Contains(result.Detail, "wl-copy") {
		t.Fatalf("detail should name the missing clipboard tool: %q", result.Detail)
	}
}

func TestPasteClipboardXclip(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"xclip": true, "xdotool": true}}
	result := PasteWith(r, "text")
	if !result.Pasted || result.Backend != "xdotool" {
		t.Fatalf("xclip+xdotool path failed: %+v", result)
	}
	if !reflect.DeepEqual(r.runs[0].args, []string{"-selection", "clipboard", "-i"}) {
		t.Fatalf("xclip args = %v", r.runs[0].args)
	}
}

func TestAvailableAndBestBackend(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"wl-copy": true, "wtype": true}}
	if !Available(r) {
		t.Fatal("Available should be true with clipboard+backend")
	}
	if got := BestBackend(r); got != "wtype" {
		t.Fatalf("BestBackend = %q, want wtype", got)
	}
	// Clipboard alone is not the full path.
	r2 := &fakeRunner{present: map[string]bool{"wl-copy": true}}
	if Available(r2) {
		t.Fatal("Available should be false without a paste backend")
	}
}

// TestExecRunnerRunStdinDoesNotBlockOnForkingClipboard guards the fix for
// the 28-second paste stall: wl-copy forks a background daemon that holds
// the selection, and the old CombinedOutput blocked on that daemon's
// inherited stdout pipe. The detached RunStdin must return as soon as the
// wl-copy parent exits (well under the 2s bound; the old code took 7-31s).
func TestExecRunnerRunStdinDoesNotBlockOnForkingClipboard(t *testing.T) {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		t.Skip("wl-copy not installed")
	}
	r := execRunner{}
	start := time.Now()
	if _, err := r.RunStdin("wl-copy", []string{"--trim-newline"}, []byte("detach-timing-probe")); err != nil {
		t.Fatalf("wl-copy failed: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("RunStdin blocked for %v; the forking clipboard daemon held the pipe", d)
	}
}
