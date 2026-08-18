//go:build darwin

package paste

import (
	"reflect"
	"strings"
	"testing"
)

// The pbcopy + osascript happy path: the exact payload reaches the
// pasteboard, then one Cmd+V keystroke is injected through System Events.
func TestPasteDarwinKeystroke(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"pbcopy": true, "osascript": true}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != BackendOsascriptKeystroke {
		t.Fatalf("darwin paste failed: %+v", result)
	}
	want := []cmdCall{
		{"pbcopy", nil},
		{"osascript", []string{"-e", keystrokeScript}},
	}
	if !reflect.DeepEqual(r.runs, want) {
		t.Fatalf("calls = %+v, want %+v", r.runs, want)
	}
	if len(r.stdin) != 1 || r.stdin[0] != "hello" {
		t.Fatalf("pasteboard stdin = %q, want %q", r.stdin, "hello")
	}
}

// A failed keystroke (the usual cause is a missing Accessibility grant)
// must degrade to a truthful clipboard-only result that tells the user how
// to enable automatic paste.
func TestPasteDarwinKeystrokeFailureIsClipboardOnly(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"pbcopy": true, "osascript": true}}
	r.failRun = map[string]bool{"osascript": true}
	result := PasteWith(r, "text")
	if result.Pasted || result.Backend != "clipboard" {
		t.Fatalf("expected truthful clipboard fallback: %+v", result)
	}
	if !strings.Contains(result.Detail, "Accessibility") {
		t.Fatalf("detail should explain the Accessibility requirement: %q", result.Detail)
	}
	// The payload still reached the pasteboard.
	if len(r.stdin) != 1 || r.stdin[0] != "text" {
		t.Fatalf("pasteboard stdin = %q, want %q", r.stdin, "text")
	}
}

// Without a clipboard tool nothing can be claimed.
func TestPasteDarwinNoClipboard(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"osascript": true}}
	result := PasteWith(r, "text")
	if result.Pasted {
		t.Fatal("must not claim success without a clipboard")
	}
	if !strings.Contains(result.Detail, "pbcopy") {
		t.Fatalf("detail should name the missing clipboard tool: %q", result.Detail)
	}
}

// pbcopy + osascript is the full paste path; neither alone suffices.
func TestDarwinAvailableAndBestBackend(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"pbcopy": true, "osascript": true}}
	if !Available(r) {
		t.Fatal("Available should be true with pbcopy+osascript")
	}
	if got := BestBackend(r); got != BackendOsascriptKeystroke {
		t.Fatalf("BestBackend = %q, want %q", got, BackendOsascriptKeystroke)
	}
	noOsa := &fakeRunner{present: map[string]bool{"pbcopy": true}}
	if Available(noOsa) || BestBackend(noOsa) != "" {
		t.Fatal("Available must be false without osascript")
	}
}
