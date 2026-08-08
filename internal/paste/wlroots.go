package paste

// Focus resolution for wlroots-family compositors (labwc, wayfire, river,
// hikari, …) that expose zwlr_foreign_toplevel_manager_v1 but have no
// dedicated IPC like hyprctl or swaymsg. A tiny raw Wayland client lists the
// toplevels and reports the app_id of the activated (focused) one.
//
// ext-foreign-toplevel-list-v1 cannot be used: it carries no focus state.
// The client is raw rather than built on go-wayland's core client because
// that library cannot register server-created objects (new_id in an event —
// the toplevel handles): its generated dispatch looks them up in the context
// object map, which only ever holds client-created ids.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Wayland protocol constants used by the probe. Object id 1 is the display.
// Client-created objects must form a dense sequence starting at 2 (the
// server validates new ids with "count < id" and kills the connection on
// gaps — wl_map_insert_at, wayland-util.c). Server-created object ids start
// at 0xff000000; the probe only reads events from those, never sends to them.
const (
	wlDisplayID = 1

	// wl_display requests
	wlGetRegistry = 1 // get_registry
	wlSync        = 0 // sync

	// wl_registry requests and wl_callback events
	wlRegistryBind = 0 // bind
	wlCallbackDone = 0 // done

	// wl_registry event
	registryGlobal = 0 // global

	// zwlr_foreign_toplevel_manager_v1 event
	zwlrToplevel = 0 // toplevel

	// zwlr_foreign_toplevel_handle_v1 events
	zwlrHandleAppID  = 1 // app_id
	zwlrHandleState  = 4 // state
	zwlrHandleClosed = 6 // closed

	zwlrStateActivated = 2 // state enum: activated

	zwlrManagerInterface = "zwlr_foreign_toplevel_manager_v1"
)

// wlrootsProbeTimeout bounds the whole focus query. The compositor answers a
// fresh toplevel list in milliseconds; the deadline only guards a dead or
// wedged socket so paste never hangs.
const wlrootsProbeTimeout = 400 * time.Millisecond

// resolveWlrootsFocus returns the focused window's class by querying the
// zwlr_foreign_toplevel_manager_v1 global of the running Wayland compositor.
// It is the generic resolver for wlroots-family compositors that lack a
// dedicated IPC. It engages only for the real runner: the query is raw
// compositor I/O that the runner abstraction cannot simulate, and fake
// runners in tests must not open a live socket. Any failure (no Wayland
// session, global absent, timeout, nothing focused) fails fast so the next
// backend runs.
func resolveWlrootsFocus(r runner) (windowTarget, bool) {
	if _, ok := r.(execRunner); !ok {
		return windowTarget{}, false
	}
	appID, ok := wlrootsFocusedAppID()
	if !ok || appID == "" {
		return windowTarget{}, false
	}
	return windowTarget{Class: strings.ToLower(appID)}, true
}

// wlrootsFocusedAppID connects to the compositor, enumerates the toplevels
// via the zwlr manager and returns the app_id of the activated (focused)
// one.
func wlrootsFocusedAppID() (string, bool) {
	conn, err := dialWayland()
	if err != nil {
		return "", false
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(wlrootsProbeTimeout)); err != nil {
		return "", false
	}

	probe := &wlrProbe{
		registryID: 2, // dense: display=1, then 2, 3, 4
		managerID:  4,
		callbackID: 3, // freed by the server after done; reused for round 2
		toplevels:  map[uint32]*wlrToplevel{},
	}

	// Round 1: enumerate globals (get_registry + sync). The sync callback
	// fires after the initial global list has been delivered.
	if err := wlRequest(conn, wlDisplayID, wlGetRegistry, uint32Args(probe.registryID)); err != nil {
		return "", false
	}
	if err := wlRequest(conn, wlDisplayID, wlSync, uint32Args(probe.callbackID)); err != nil {
		return "", false
	}
	for !probe.callbackDone {
		if err := probe.dispatch(conn); err != nil {
			return "", false
		}
	}
	if probe.managerName == 0 {
		return "", false
	}
	probe.callbackDone = false

	// Round 2: bind the manager. The initial toplevel list (with full state)
	// is emitted atomically in response to the bind; the sync callback marks
	// the end of it.
	version := probe.managerVersion
	if version > 3 {
		version = 3
	}
	if err := wlRequest(conn, probe.registryID, wlRegistryBind,
		uint32Args(probe.managerName),
		stringBytes(zwlrManagerInterface),
		uint32Args(version, probe.managerID)); err != nil {
		return "", false
	}
	if err := wlRequest(conn, wlDisplayID, wlSync, uint32Args(probe.callbackID)); err != nil {
		return "", false
	}
	for !probe.callbackDone {
		if err := probe.dispatch(conn); err != nil {
			return "", false
		}
	}

	return focusedToplevelAppID(probe.toplevels)
}

// wlrProbe is the client state of one focus query.
type wlrProbe struct {
	registryID     uint32
	managerID      uint32
	callbackID     uint32
	managerName    uint32 // registry name of the zwlr global (0 = absent)
	managerVersion uint32
	callbackDone   bool
	toplevels      map[uint32]*wlrToplevel
}

// wlrToplevel is the subset of zwlr_foreign_toplevel_handle_v1 state the
// probe needs.
type wlrToplevel struct {
	appID     string
	activated bool
}

// dispatch reads and handles one event.
func (p *wlrProbe) dispatch(conn *net.UnixConn) error {
	sender, opcode, data, err := wlRead(conn)
	if err != nil {
		return err
	}
	switch {
	case sender == wlDisplayID:
		// wl_display.error / delete_id: nothing to act on.
	case sender == p.registryID:
		if opcode == registryGlobal {
			if name, iface, version := parseGlobal(data); iface == zwlrManagerInterface {
				p.managerName = name
				p.managerVersion = version
			}
		}
	case sender == p.callbackID:
		if opcode == wlCallbackDone {
			p.callbackDone = true
		}
	case sender == p.managerID:
		if opcode == zwlrToplevel {
			id := binary.LittleEndian.Uint32(data[0:4])
			p.toplevels[id] = &wlrToplevel{}
		}
	default:
		if t, ok := p.toplevels[sender]; ok {
			switch opcode {
			case zwlrHandleAppID:
				t.appID, _ = parseString(data, 0)
			case zwlrHandleState:
				stateBytes, _ := parseArray(data, 0)
				t.activated = stateActivated(stateBytes)
			case zwlrHandleClosed:
				delete(p.toplevels, sender)
			}
		}
	}
	return nil
}

// focusedToplevelAppID returns the app_id of the activated toplevel. A
// desktop with no focused window reports no activated toplevel — the paste
// must not target a random window, so that is a miss.
func focusedToplevelAppID(ts map[uint32]*wlrToplevel) (string, bool) {
	for _, t := range ts {
		if t.activated {
			return t.appID, true
		}
	}
	return "", false
}

// stateActivated reports whether a zwlr_foreign_toplevel_handle_v1 state
// array contains the activated (focused) flag.
func stateActivated(state []byte) bool {
	for i := 0; i+4 <= len(state); i += 4 {
		if binary.LittleEndian.Uint32(state[i:i+4]) == zwlrStateActivated {
			return true
		}
	}
	return false
}

// parseGlobal decodes a wl_registry.global event.
func parseGlobal(data []byte) (name uint32, iface string, version uint32) {
	name = binary.LittleEndian.Uint32(data[0:4])
	iface, off := parseString(data, 4)
	version = binary.LittleEndian.Uint32(data[off : off+4])
	return name, iface, version
}

// parseString decodes a wayland string argument starting at off. The length
// field is the raw string length (NUL included); the argument occupies
// pad4(length) bytes. Returns the string and the offset past the argument.
func parseString(data []byte, off int) (string, int) {
	length := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	s := data[off : off+length]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return string(s), off + pad4(length)
}

// parseArray decodes a wayland array argument starting at off and returns
// its contents and the offset past the argument. Array lengths are byte
// counts; uint32 arrays (the only kind this probe reads) are 4-aligned.
func parseArray(data []byte, off int) ([]byte, int) {
	length := int(binary.LittleEndian.Uint32(data[off : off+4]))
	off += 4
	return data[off : off+length], off + length
}

// dialWayland connects to the compositor socket named by WAYLAND_DISPLAY
// (default wayland-0), mirroring the core wayland client's resolution.
func dialWayland() (*net.UnixConn, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return nil, fmt.Errorf("wayland: XDG_RUNTIME_DIR not set")
	}
	addr := os.Getenv("WAYLAND_DISPLAY")
	if addr == "" {
		addr = "wayland-0"
	}
	return net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(runtimeDir, addr), Net: "unix"})
}

// wlRequest writes one request: the 8-byte header (sender id, size<<16 |
// opcode) followed by the marshaled arguments.
func wlRequest(conn *net.UnixConn, sender, opcode uint32, args ...[]byte) error {
	size := 8
	for _, a := range args {
		size += len(a)
	}
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], sender)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(size)<<16|opcode)
	off := 8
	for _, a := range args {
		copy(buf[off:], a)
		off += len(a)
	}
	_, err := conn.Write(buf)
	return err
}

// wlRead reads one event: the 8-byte header plus the message body.
func wlRead(conn *net.UnixConn) (sender, opcode uint32, data []byte, err error) {
	var header [8]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil {
		return 0, 0, nil, err
	}
	sender = binary.LittleEndian.Uint32(header[0:4])
	w := binary.LittleEndian.Uint32(header[4:8])
	opcode = w & 0xffff
	size := int(w >> 16)
	if size < 8 {
		return 0, 0, nil, fmt.Errorf("wayland: invalid message size %d", size)
	}
	data = make([]byte, size-8)
	if _, err = io.ReadFull(conn, data); err != nil {
		return 0, 0, nil, err
	}
	return sender, opcode, data, nil
}

// uint32Args marshals uint32 request arguments.
func uint32Args(vs ...uint32) []byte {
	buf := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.LittleEndian.PutUint32(buf[4*i:], v)
	}
	return buf
}

// stringBytes marshals a wayland string argument. The length field is the
// raw byte count including the terminating NUL (per the wire spec); the
// argument occupies pad4(length) bytes. Sending the padded length instead
// (as go-wayland's generated client does) trips wayland-server >= 1.24's
// embedded-NUL check ("string has embedded nul at offset ...", EINVAL) and
// gets the connection killed.
func stringBytes(s string) []byte {
	raw := len(s) + 1
	buf := make([]byte, 4+pad4(raw))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(raw))
	copy(buf[4:], s)
	return buf
}

func pad4(n int) int {
	if n&3 != 0 {
		return n + 4 - n&3
	}
	return n
}
