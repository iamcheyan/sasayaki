package paste

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every command and lets tests control LookPath results,
// per-call output (onRun) and failures (failRun/failStdin).
type fakeRunner struct {
	present   map[string]bool
	runs      []cmdCall
	stdin     []string
	failRun   map[string]bool
	failStdin map[string]bool
	onRun     func(name string, args []string) ([]byte, error)
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
	if f.onRun != nil {
		return f.onRun(name, args)
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

// hyprRunner is a runner with the standard desktop toolset plus a Hyprland
// activewindow response.
func hyprRunner(class string, xwayland bool) *fakeRunner {
	return &fakeRunner{present: map[string]bool{
		"wl-copy": true, "wtype": true, "hyprctl": true,
	}, onRun: func(name string, args []string) ([]byte, error) {
		if name == "hyprctl" && len(args) == 2 && args[1] == "activewindow" {
			return []byte(`{"class":"` + class + `","address":"0xaaabc832aea0","pid":4949,"xwayland":` + boolStr(xwayland) + `}`), nil
		}
		return nil, nil
	}}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// The GUI default (no Hyprland to resolve focus, class unknown) must try
// Ctrl+V first — the chord GUI apps actually bind — not Ctrl+Shift+V.
func TestPasteGuiCtrlV(t *testing.T) {
	r := fullRunner()
	result := PasteWith(r, "hello")
	if !result.Pasted {
		t.Fatalf("PasteWith = %+v, want pasted", result)
	}
	if result.Backend != "wtype" {
		t.Fatalf("backend = %q, want wtype", result.Backend)
	}
	// Clipboard first: wl-copy with --trim-newline and the exact text.
	// Then the wtype Ctrl+V chord.
	want := []cmdCall{
		{"wl-copy", []string{"--trim-newline"}},
		{"wtype", []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}},
	}
	if !reflect.DeepEqual(r.runs, want) {
		t.Fatalf("calls = %+v, want %+v", r.runs, want)
	}
	if len(r.stdin) != 1 || r.stdin[0] != "hello" {
		t.Fatalf("clipboard stdin = %q, want %q", r.stdin, "hello")
	}
}

// A native GUI window keeps the Ctrl+V-first order even when its class is
// known (Firefox).
func TestPasteNativeGuiClassCtrlV(t *testing.T) {
	r := hyprRunner("org.mozilla.firefox", false)
	result := PasteWith(r, "text")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("native GUI paste failed: %+v", result)
	}
	if got := r.runs[len(r.runs)-1]; got.name != "wtype" ||
		!reflect.DeepEqual(got.args, []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}) {
		t.Fatalf("expected Ctrl+V first, got %+v", got)
	}
}

// foot binds Shift+Insert as its primary paste shortcut.
func TestPasteTerminalFootShiftInsertFirst(t *testing.T) {
	r := hyprRunner("foot", false)
	result := PasteWith(r, "text")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("foot paste failed: %+v", result)
	}
	if got := r.runs[len(r.runs)-1]; got.name != "wtype" ||
		!reflect.DeepEqual(got.args, []string{"-M", "shift", "-k", "Insert", "-m", "shift"}) {
		t.Fatalf("expected Shift+Insert first for foot, got %+v", got)
	}
}

// alacritty binds Ctrl+Shift+V as its primary paste shortcut.
func TestPasteTerminalAlacrittyCtrlShiftVFirst(t *testing.T) {
	r := hyprRunner("Alacritty", false)
	result := PasteWith(r, "text")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("alacritty paste failed: %+v", result)
	}
	if got := r.runs[len(r.runs)-1]; got.name != "wtype" ||
		!reflect.DeepEqual(got.args, []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"}) {
		t.Fatalf("expected Ctrl+Shift+V first for alacritty, got %+v", got)
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
	// The chord attempts in order: wtype → ydotool → xdotool (no hyprctl).
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

// Hyprland send_key_state is the fallback when wtype/ydotool fail; it must
// target the focused window explicitly.
func TestPasteHyprlandSendKeyFallback(t *testing.T) {
	r := hyprRunner("org.kde.kwrite", false)
	r.present["ydotool"] = true
	r.failRun = map[string]bool{"wtype": true, "ydotool": true}
	result := PasteWith(r, "text")
	if !result.Pasted || result.Backend != "hyprland" {
		t.Fatalf("hyprland fallback failed: %+v", result)
	}
	var dispatches []string
	for _, call := range r.runs {
		if call.name == "hyprctl" && len(call.args) == 2 && call.args[0] == "dispatch" {
			dispatches = append(dispatches, call.args[1])
		}
	}
	want := []string{
		`hl.dsp.send_key_state({ mods = "CTRL", key = "V", state = "down", window = "0xaaabc832aea0" })`,
		`hl.dsp.send_key_state({ mods = "CTRL", key = "V", state = "up", window = "0xaaabc832aea0" })`,
	}
	if !reflect.DeepEqual(dispatches, want) {
		t.Fatalf("dispatches = %v, want %v", dispatches, want)
	}
}

// XWayland windows need the X11 clipboard (xsel) plus an XTEST injection —
// Wayland virtual-keyboard tools cannot reach them, and the payload must be
// on the X11 CLIPBOARD for the paste to read anything.
func TestPasteXWayland(t *testing.T) {
	r := hyprRunner("wechat", true)
	r.present["xsel"] = true
	r.present["xdotool"] = true
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "xdotool-x11" {
		t.Fatalf("xwayland paste failed: %+v", result)
	}
	// Payload reached both clipboards (Wayland wl-copy + X11 xsel).
	if len(r.stdin) != 2 || r.stdin[0] != "hello" || r.stdin[1] != "hello" {
		t.Fatalf("clipboard writes = %q, want hello on both Wayland and X11", r.stdin)
	}
	// XTEST injection (no --window): delivered to the focused X window and
	// accepted by GTK, unlike synthetic --window keys GTK silently drops.
	last := r.runs[len(r.runs)-1]
	if !reflect.DeepEqual(last.args, []string{"key", "--clearmodifiers", "ctrl+v"}) {
		t.Fatalf("xdotool args = %v", last.args)
	}
}

// kitty gets a native remote paste: one bracketed transaction, resolved to
// the exact window id so the payload can never land in two kitty windows.
func TestPasteKittyRemoteNative(t *testing.T) {
	r := hyprRunner("kitty", false)
	r.present["wl-copy"] = true
	r.present["kitty"] = true
	r.onRun = func(name string, args []string) ([]byte, error) {
		switch {
		case name == "hyprctl" && len(args) == 2 && args[1] == "activewindow":
			return []byte(`{"class":"kitty","address":"0xaaabc798ce10","pid":4321,"xwayland":false}`), nil
		case name == "kitty" && len(args) == 4 && args[1] == "--to" && args[3] == "ls":
			return []byte(`[{"is_focused":true,"tabs":[{"is_active":true,"windows":[{"is_focused":true,"id":42}]}]}]`), nil
		}
		return nil, nil
	}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "kitty-native-paste" {
		t.Fatalf("kitty remote paste failed: %+v", result)
	}
	wantAction := []string{"@", "--to", "unix:/tmp/mykitty-4321", "action", "--match", "id:42", "paste_from_clipboard"}
	found := false
	for _, call := range r.runs {
		if call.name == "kitty" && reflect.DeepEqual(call.args, wantAction) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected kitty action %v in %+v", wantAction, r.runs)
	}
}

// kitty bracketed fallback: clipboard cleared first so OSC 5522 cannot
// double-paste, then restored with the payload.
func TestPasteKittyRemoteBracketedFallback(t *testing.T) {
	r := hyprRunner("kitty", false)
	r.present["wl-copy"] = true
	r.present["kitty"] = true
	r.onRun = func(name string, args []string) ([]byte, error) {
		switch {
		case name == "hyprctl" && len(args) == 2 && args[1] == "activewindow":
			return []byte(`{"class":"kitty","pid":4321,"xwayland":false}`), nil
		case name == "kitty" && len(args) == 4 && args[1] == "--to" && args[3] == "ls":
			return []byte(`[{"tabs":[{"windows":[{"id":42}]}]}]`), nil
		case name == "kitty" && len(args) > 1 && args[1] == "--to" && args[3] == "action":
			return nil, &runError{"kitty"}
		}
		return nil, nil
	}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "kitty-bracketed-fallback" {
		t.Fatalf("kitty bracketed fallback failed: %+v", result)
	}
	// wl-copy -c then send-text with the payload, then restore.
	// stdin order: initial wl-copy, kitty send-text, wl-copy restore.
	var clears, sends int
	for _, call := range r.runs {
		if call.name == "wl-copy" && reflect.DeepEqual(call.args, []string{"-c"}) {
			clears++
		}
		if call.name == "kitty" && len(call.args) > 3 && call.args[3] == "send-text" {
			sends++
		}
	}
	if clears != 1 || sends != 1 {
		t.Fatalf("clears=%d sends=%d, want 1/1", clears, sends)
	}
	if len(r.stdin) != 3 || r.stdin[0] != "hello" || r.stdin[1] != "hello" || r.stdin[2] != "hello" {
		t.Fatalf("clipboard/send-text stdin = %q, want payload on all three writes", r.stdin)
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

// ---- focused-window resolution backends ----

// swayTree builds a minimal `swaymsg -t get_tree` document with one focused
// window whose identity is set by the closure.
func swayTree(focused func(*swayNode)) string {
	n := swayNode{Focused: true, AppID: new("foot"), Shell: "xdg_shell", PID: 4949}
	focused(&n)
	return `{"focused":false,"nodes":[` + mustJSON(n) + `]}`
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// A focused native Wayland window under Sway must resolve to the foot class
// and use the Shift+Insert chord terminals bind.
func TestResolveSwayTerminalShiftInsert(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"swaymsg": true, "wl-copy": true, "wtype": true},
		onRun: func(name string, args []string) ([]byte, error) {
			if name == "swaymsg" {
				return []byte(swayTree(func(n *swayNode) {})), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("PasteWith = %+v, want wtype paste", result)
	}
	// Clipboard first, then the focus query, then the terminal chord.
	if len(r.runs) < 3 {
		t.Fatalf("calls = %+v, want wl-copy + swaymsg + wtype", r.runs)
	}
	if got := r.runs[1]; got.name != "swaymsg" || !reflect.DeepEqual(got.args, []string{"-t", "get_tree"}) {
		t.Fatalf("focus query = %+v, want swaymsg -t get_tree", got)
	}
	last := r.runs[len(r.runs)-1]
	wantChord := []string{"-M", "shift", "-k", "Insert", "-m", "shift"}
	if last.name != "wtype" || !reflect.DeepEqual(last.args, wantChord) {
		t.Fatalf("chord = %+v, want wtype %v", last, wantChord)
	}
}

// A focused XWayland window under Sway must take the xsel+xdotool path,
// because Wayland virtual-keyboard tools cannot reach X windows.
func TestResolveSwayXWayland(t *testing.T) {
	r := &fakeRunner{present: map[string]bool{"swaymsg": true, "wl-copy": true, "xsel": true, "xdotool": true},
		onRun: func(name string, args []string) ([]byte, error) {
			if name == "swaymsg" {
				return []byte(swayTree(func(n *swayNode) {
					n.AppID = nil
					n.WindowProps = &swayWinProps{Class: "Firefox"}
					n.Shell = "xwayland"
				})), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "xdotool-x11" {
		t.Fatalf("PasteWith = %+v, want xdotool-x11 paste", result)
	}
	var sawXsel, sawXdotool bool
	for _, c := range r.runs {
		if c.name == "xsel" {
			sawXsel = true
		}
		if c.name == "xdotool" && len(c.args) == 3 && c.args[2] == "ctrl+v" {
			sawXdotool = true
		}
	}
	if !sawXsel || !sawXdotool {
		t.Fatalf("calls = %+v, want xsel clipboard + xdotool ctrl+v", r.runs)
	}
}

// KWin: the scripting plugin is loaded and run over D-Bus, its js: output is
// scraped from the journal (`journalctl -o cat` prints the bare message, so
// the lines are exactly `js: <class>`), and the plugin is unloaded again. A
// foot window must paste with Shift+Insert.
func TestResolveKWinTerminalShiftInsert(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	r := &fakeRunner{present: map[string]bool{"qdbus6": true, "journalctl": true, "wl-copy": true, "wtype": true},
		onRun: func(name string, args []string) ([]byte, error) {
			switch {
			case name == "qdbus6" && len(args) == 5 && args[2] == "org.kde.kwin.Scripting.loadScript":
				return []byte("Script42"), nil
			case name == "journalctl":
				return []byte("js: foot\njs: 4949\n"), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("PasteWith = %+v, want wtype paste", result)
	}
	var ran, unloaded bool
	for _, c := range r.runs {
		if c.name == "qdbus6" && len(c.args) == 5 && c.args[2] == "org.kde.kwin.Scripting.loadScript" {
			ran = true
		}
		if c.name == "qdbus6" && len(c.args) == 4 && c.args[2] == "org.kde.kwin.Scripting.unloadScript" {
			unloaded = true
		}
	}
	if !ran || !unloaded {
		t.Fatalf("calls = %+v, want loadScript and unloadScript", r.runs)
	}
	last := r.runs[len(r.runs)-1]
	wantChord := []string{"-M", "shift", "-k", "Insert", "-m", "shift"}
	if last.name != "wtype" || !reflect.DeepEqual(last.args, wantChord) {
		t.Fatalf("chord = %+v, want wtype %v", last, wantChord)
	}
}

// Under a Wayland session, an X window found by the EWMH probe is an
// XWayland window and must use the xsel+xdotool path.
func TestResolveX11XWayland(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	r := &fakeRunner{present: map[string]bool{"xprop": true, "wl-copy": true, "xsel": true, "xdotool": true},
		onRun: func(name string, args []string) ([]byte, error) {
			switch {
			case name == "xprop" && len(args) == 2:
				return []byte(`_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3e00005`), nil
			case name == "xprop" && len(args) == 4:
				return []byte("WM_CLASS(STRING) = \"Navigator\", \"firefox\""), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "xdotool-x11" {
		t.Fatalf("PasteWith = %+v, want xdotool-x11 paste", result)
	}
}

// The EWMH probe resolves the focused window's class and pid from xprop.
// Under a Wayland session the probed X window is an XWayland window; on
// plain X11 it is a native one. resolveFocus is tested directly so the
// Wayland/X11 split is controlled by the caller, not by PasteWith's
// graphical-environment probe (which re-derives the socket from XDG_RUNTIME_DIR).
func TestResolveX11Focus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		wayland      string
		wantXWayland bool
	}{
		{"plain-x11", "", false},
		{"wayland-session", "wayland-0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WAYLAND_DISPLAY", tc.wayland)
			t.Setenv("XDG_CURRENT_DESKTOP", "Hyprland")
			r := &fakeRunner{present: map[string]bool{"xprop": true},
				onRun: func(name string, args []string) ([]byte, error) {
					switch {
					case name == "xprop" && len(args) == 2: // -root _NET_ACTIVE_WINDOW
						return []byte(`_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3e00005`), nil
					case name == "xprop" && len(args) == 4: // -id <win> WM_CLASS _NET_WM_PID
						return []byte("WM_CLASS(STRING) = \"Navigator\", \"firefox\"\n_NET_WM_PID(CARDINAL) = 4949"), nil
					}
					return nil, nil
				}}
			target := resolveFocus(r)
			if target.Class != "firefox" || target.Pid != 4949 || target.XWayland != tc.wantXWayland {
				t.Fatalf("resolveFocus = %+v, want class=firefox pid=4949 xwayland=%v", target, tc.wantXWayland)
			}
		})
	}
}

// KWin script racing the compositor's own runtime (class "js::…") must be
// treated as a miss so the generic Ctrl+V path is used instead of a bogus
// class.
func TestResolveKWinScriptNoiseIgnored(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "Plasma")
	r := &fakeRunner{present: map[string]bool{"qdbus": true, "journalctl": true, "wl-copy": true, "wtype": true},
		onRun: func(name string, args []string) ([]byte, error) {
			switch {
			case name == "qdbus" && len(args) == 5 && args[2] == "org.kde.kwin.Scripting.loadScript":
				return []byte("7"), nil // numeric id: caller must prefix Script
			case name == "journalctl":
				return []byte("Aug 03 12:00:00 host js: js::sasayaki_1234\nAug 03 12:00:01 host js: 9999\n"), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("PasteWith = %+v, want generic wtype paste", result)
	}
	last := r.runs[len(r.runs)-1]
	wantChord := []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}
	if !reflect.DeepEqual(last.args, wantChord) {
		t.Fatalf("chord = %+v, want generic Ctrl+V %v", last, wantChord)
	}
}

// GNOME: the window-calls-extended shell extension answers FocusClass over
// D-Bus; a GUI class keeps the Ctrl+V-first order.
func TestResolveGNOMEGuiCtrlV(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "GNOME")
	r := &fakeRunner{present: map[string]bool{"gdbus": true, "wl-copy": true, "wtype": true},
		onRun: func(name string, args []string) ([]byte, error) {
			if name == "gdbus" {
				return []byte("(<'firefox'>)"), nil
			}
			return nil, nil
		}}
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("PasteWith = %+v, want wtype paste", result)
	}
	last := r.runs[len(r.runs)-1]
	wantChord := []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}
	if !reflect.DeepEqual(last.args, wantChord) {
		t.Fatalf("chord = %+v, want Ctrl+V %v", last, wantChord)
	}
}

// A non-KDE/non-GNOME session with no compositor IPC and no X11 falls back
// to the generic Ctrl+V chord for an unknown window (the long-standing GUI
// default).
func TestResolveFocusUnknownWindowGeneric(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "Hyprland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	r := fullRunner()
	result := PasteWith(r, "hello")
	if !result.Pasted || result.Backend != "wtype" {
		t.Fatalf("PasteWith = %+v, want wtype paste", result)
	}
	want := []cmdCall{
		{"wl-copy", []string{"--trim-newline"}},
		{"wtype", []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}},
	}
	if !reflect.DeepEqual(r.runs, want) {
		t.Fatalf("calls = %+v, want %+v", r.runs, want)
	}
}
