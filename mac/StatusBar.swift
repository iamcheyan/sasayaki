//
//  StatusBar.swift — Sasayaki voice input menu bar app (macOS only)
//
//  Build:  swiftc -O -o build/sasayaki-menubar mac/StatusBar.swift
//  Run:    open dist/Sasayaki.app
//
//  Menu bar mic icon + global hotkeys:
//    F13 — toggle voice input: record → transcribe → paste
//    F14 — toggle translation: record → transcribe → translate → paste
//
//  States: idle / recording (white waveform, blinking) / transcribing
//  (orange waveform) / translating (blue globe) / succeeded / failed
//  (transient).
//
//  The app records natively via AVAudioEngine (the TCC mic grant follows
//  the process that records) and hands the finalized WAV to the sibling
//  `sasayaki` Go binary bundled at Contents/MacOS/sasayaki:
//    F13 stop  → `sasayaki deliver <wav> --json`
//    F14 stop  → `sasayaki deliver <wav> --translate --json`
//  The service transcribes and pastes; the app polls
//  `sasayaki status --json` for the phase. While the app records natively
//  the APP is the phase authority (the service is idle then).
//
//  All process work runs OFF the main thread. Long actions never get a
//  stdout pipe: a backgrounded recorder inheriting the pipe's write end
//  blocks readDataToEndOfFile until the recording ends, which froze the
//  toggle state machine ("stuck transcribing" bug). deliver/status are
//  fast socket round-trips (3 s deadline in the CLI) and may be captured.
//

import Cocoa
import Carbon.HIToolbox
import AVFAudio
import ApplicationServices

// Resolve the sibling `sasayaki` CLI from this executable's path (the Go
// binary lives next to the menubar binary inside the app bundle).
let execPath = CommandLine.arguments.first ?? ""
let binDir: String = {
    var url = URL(fileURLWithPath: execPath)
    if url.path.isEmpty { return FileManager.default.currentDirectoryPath }
    url.deleteLastPathComponent()
    return url.path
}()
let cli = binDir + "/sasayaki"
let configPath = NSHomeDirectory() + "/.config/sasayaki/config.json"
// Staging dir for in-flight native recordings; deliver moves the finished
// WAV into the service's recordings dir, so retention cleanup owns it.
let recDir = NSHomeDirectory() + "/.cache/sasayaki/run"

/// Native in-process recorder. The TCC microphone grant obtained via
/// requestRecordPermission applies to this process's AVAudioEngine — unlike
/// a spawned ffmpeg, which macOS attributes separately and silently feeds
/// all-zero samples when the chain doesn't resolve.
final class Recorder: NSObject {
    private let engine = AVAudioEngine()
    private var file: AVAudioFile?
    private var tmpURL: URL?
    private var converter: AVAudioConverter?
    private var dstFormat: AVAudioFormat?
    private(set) var running = false

    func start() -> Bool {
        guard !running else { return true }
        let input = engine.inputNode
        let inFormat = input.inputFormat(forBus: 0)
        guard inFormat.sampleRate > 0, inFormat.channelCount > 0 else {
            try? "\(Date()) bad input format: \(inFormat)".write(toFile: NSHomeDirectory() + "/.cache/sasayaki/mic-debug.log", atomically: true, encoding: .utf8)
            return false
        }

        // Convert in the tap to deinterleaved float32 @16k/mono — exactly the
        // AVAudioFile processing format. The FILE settings (Int16 PCM below)
        // handle the on-disk encoding; writing a float32 buffer is the only
        // format AVAudioFile.write accepts without tripping caulk asserts.
        guard let dst = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                      sampleRate: 16000, channels: 1,
                                      interleaved: false),
              let conv = AVAudioConverter(from: inFormat, to: dst)
        else { return false }

        let dir = URL(fileURLWithPath: recDir)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let tmp = dir.appendingPathComponent("rec-\(Int(Date().timeIntervalSince1970)).wav")
        guard let out = try? AVAudioFile(forWriting: tmp, settings: [
            AVFormatIDKey: kAudioFormatLinearPCM,
            AVSampleRateKey: 16000,
            AVNumberOfChannelsKey: 1,
            AVLinearPCMBitDepthKey: 16,
            AVLinearPCMIsFloatKey: false,
            AVLinearPCMIsBigEndianKey: false,
        ]) else { return false }

        file = out
        tmpURL = tmp
        converter = conv
        dstFormat = dst

        input.installTap(onBus: 0, bufferSize: 4096, format: inFormat) { [weak self] buf, _ in
            guard let self = self, let file = self.file, let dst = self.dstFormat
            else { return }
            let ratio = 16000.0 / inFormat.sampleRate
            let cap = AVAudioFrameCount((Double(buf.frameLength) * ratio).rounded(.up)) + 64
            guard let outBuf = AVAudioPCMBuffer(pcmFormat: dst, frameCapacity: cap)
            else { return }

            // Feed the tap buffer EXACTLY once per convert call; further
            // pulls get .noDataNow. Claiming .haveData with a spent buffer
            // makes ExtAudioFile abort the process (caulk assert — observed
            // crash log).
            var fed = false
            _ = conv.convert(to: outBuf, error: nil) { _, status in
                if fed {
                    status.pointee = .noDataNow
                    return nil
                }
                fed = true
                status.pointee = .haveData
                return buf
            }
            if outBuf.frameLength > 0 {
                try? file.write(from: outBuf)
            }
        }
        do {
            engine.prepare()
            try engine.start()
            running = true
            return true
        } catch {
            input.removeTap(onBus: 0)
            file = nil
            tmpURL = nil
            converter = nil
            dstFormat = nil
            try? "\(Date()) engine.start failed: \(error)".write(toFile: NSHomeDirectory() + "/.cache/sasayaki/mic-debug.log", atomically: true, encoding: .utf8)
            return false
        }
    }

    /// Stop and finalize. completion(nil) when nothing was captured.
    func stop(completion: @escaping (URL?) -> Void) {
        guard running else { completion(nil); return }
        running = false
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        let url = tmpURL
        file = nil
        tmpURL = nil
        converter = nil
        dstFormat = nil
        // A tiny delay lets the last tap callback drain into the file.
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.1) {
            completion(url)
        }
    }

    func cancel() {
        guard running else { return }
        running = false
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        if let url = tmpURL { try? FileManager.default.removeItem(at: url) }
        file = nil
        tmpURL = nil
        converter = nil
        dstFormat = nil
    }
}

final class VoiceMenu: NSObject {
    private let recorder = Recorder()
    private let item: NSStatusItem
    private var timer: Timer?
    private var state = "idle"   // idle | recording | transcribing | translating | succeeded | failed
    private var polling = false
    private var lastFire: Date?
    private var blinkTimer: Timer?
    private var blinkOn = true
    private var launchItem: NSMenuItem?
    private var toggleItem: NSMenuItem?
    private var cancelItem: NSMenuItem?
    // Terminal phases (succeeded/failed) persist in the service until the
    // next operation; show each outcome exactly once, then return to idle.
    private var terminalTimer: Timer?
    private var lastTerminalPhase: String?
    // Optimistic hold: keep the busy icon right after a deliver while the
    // service round-trip lands (prevents a busy→idle→busy flash).
    private var optimisticUntil = Date.distantPast

    override init() {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()
        item.button?.image = NSImage(systemSymbolName: "mic", accessibilityDescription: "语音输入")
        item.button?.image?.isTemplate = true
        item.menu = buildMenu()
        render()
        timer = Timer.scheduledTimer(withTimeInterval: 0.6, repeats: true) { [weak self] _ in
            self?.pollStatus()
        }
        registerHotkeys()
        requestMicPermission()
        checkAccessibility()
    }

    // ── Menu ──────────────────────────────────────────────────────────────

    private func buildMenu() -> NSMenu {
        let m = NSMenu()
        m.autoenablesItems = false

        let titleItem = NSMenuItem(title: "语音输入", action: nil, keyEquivalent: "")
        titleItem.isEnabled = false
        m.addItem(titleItem)

        let hint = NSMenuItem(title: "F13 语音 · F14 翻译", action: nil, keyEquivalent: "")
        hint.isEnabled = false
        m.addItem(hint)
        m.addItem(.separator())

        let t = NSMenuItem(title: "开始录音 (F13)", action: #selector(toggle), keyEquivalent: "")
        t.target = self
        m.addItem(t)
        toggleItem = t

        let c = NSMenuItem(title: "取消", action: #selector(cancel), keyEquivalent: "")
        c.target = self
        m.addItem(c)
        cancelItem = c

        m.addItem(.separator())

        // ↓ Right-click menu parity with bar/SasayakiContextMenu.qml (minus
        // the wake-key toggles — F13/F14 replace them on macOS).
        let tuiItem = NSMenuItem(title: "Control Center (TUI)", action: #selector(openControlCenter), keyEquivalent: "")
        tuiItem.target = self
        m.addItem(tuiItem)

        let repairItem = NSMenuItem(title: "Repair Everything", action: #selector(runRepair), keyEquivalent: "")
        repairItem.target = self
        m.addItem(repairItem)

        m.addItem(.separator())
        let editItem = NSMenuItem(title: "Edit Configuration", action: #selector(editConfigFile), keyEquivalent: "")
        editItem.target = self
        m.addItem(editItem)

        let restartItem = NSMenuItem(title: "Service Restart", action: #selector(serviceRestart), keyEquivalent: "")
        restartItem.target = self
        m.addItem(restartItem)

        m.addItem(.separator())
        let li = NSMenuItem(title: "开机自动启动", action: #selector(toggleLaunchAtLogin), keyEquivalent: "")
        li.target = self
        launchItem = li
        m.addItem(li)

        m.addItem(.separator())
        let quitItem = NSMenuItem(title: "退出", action: #selector(quit), keyEquivalent: "")
        quitItem.target = self
        m.addItem(quitItem)
        return m
    }

    // ── Process helpers (background only) ─────────────────────────────────

    /// Capture stdout (fast socket round-trips only: status, deliver).
    /// Never used for long-running actions.
    private func capture(_ cmd: String) -> String {
        let p = Process()
        p.launchPath = "/bin/sh"
        p.arguments = ["-c", cmd]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        do { try p.run() } catch { return "" }
        p.waitUntilExit()
        return String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
    }

    /// Capture stdout plus the exit status (repair, deliver diagnostics).
    private func captureStatus(_ cmd: String) -> (String, Int32) {
        let p = Process()
        p.launchPath = "/bin/sh"
        p.arguments = ["-c", cmd]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        do { try p.run() } catch { return ("", -1) }
        p.waitUntilExit()
        return (String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? "",
                p.terminationStatus)
    }

    /// Fire-and-forget: NO stdout pipe, NO waiting. The service's own phase
    /// is the authority (its StillTranscribing guard ignores re-entrant
    /// toggles); waiting here was the "stuck" bug — any block in the child
    /// wedged the firing guard and swallowed the next hotkey press.
    private func fire(_ cmd: String) {
        let p = Process()
        p.launchPath = "/bin/sh"
        p.arguments = ["-c", cmd]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        try? p.run()
        // no waitUntilExit — the poll timer observes state changes
    }

    /// Fire-and-forget CLI call with a 250 ms burst-debounce (hotkey
    /// auto-repeat / double clicks).
    private func fireCli(_ sub: String) {
        let now = Date()
        if let last = lastFire, now.timeIntervalSince(last) < 0.25 { return }
        lastFire = now
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }
            self.fire("'\(cli)' \(sub) 2>/dev/null")
        }
    }

    /// PATH lookup, mirrors `command -v`.
    private func which(_ name: String) -> String? {
        if name.contains("/") {
            return FileManager.default.isExecutableFile(atPath: name) ? name : nil
        }
        let path = ProcessInfo.processInfo.environment["PATH"] ?? "/usr/bin:/bin"
        for dir in path.split(separator: ":") {
            let full = dir + "/" + name
            if FileManager.default.isExecutableFile(atPath: full) { return full }
        }
        return nil
    }

    /// Open a command in a terminal window: kitty first (own window via
    /// --title — the macOS kitty build has no --class/--name, those are
    /// X11-only), Terminal.app as fallback. Swift port of the darwin path
    /// in sumika-launch-tui.
    private func launchInTerminal(appID: String, _ argv: [String]) {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }
            var kitty: String?
            for cand in ["/Applications/kitty.app/Contents/MacOS/kitty",
                         NSHomeDirectory() + "/Applications/kitty.app/Contents/MacOS/kitty"] {
                if FileManager.default.isExecutableFile(atPath: cand) { kitty = cand; break }
            }
            if kitty == nil { kitty = self.which("kitty") }
            if let kitty = kitty {
                let p = Process()
                p.launchPath = kitty
                p.arguments = ["--title", appID] + argv
                p.standardOutput = FileHandle.nullDevice
                p.standardError = FileHandle.nullDevice
                try? p.run()
                return
            }
            // Terminal.app fallback: run via do script; escape for
            // AppleScript strings.
            let esc = { (s: String) in
                s.replacingOccurrences(of: "\\", with: "\\\\")
                    .replacingOccurrences(of: "\"", with: "\\\"")
            }
            let line = argv.map(esc).joined(separator: " ")
            let p = Process()
            p.launchPath = "/usr/bin/osascript"
            p.arguments = ["-e", "tell application \"Terminal\"\nactivate\ndo script \"\(line)\"\nend tell"]
            p.standardOutput = FileHandle.nullDevice
            p.standardError = FileHandle.nullDevice
            try? p.run()
        }
    }

    // ── Status poll ───────────────────────────────────────────────────────

    private func pollStatus() {
        guard !polling else { return }
        polling = true
        DispatchQueue.global(qos: .utility).async { [weak self] in
            guard let self = self else { return }
            let out = self.capture("'\(cli)' status --json 2>/dev/null")
            DispatchQueue.main.async {
                self.polling = false
                self.applyStatus(out)
            }
        }
    }

    /// Map a `sasayaki status --json` payload (protocol.State) onto the
    /// icon state. Mirrors the phase→state mapping in SasayakiInput.qml.
    private func applyStatus(_ out: String) {
        guard let obj = (try? JSONSerialization.jsonObject(with: Data(out.utf8))) as? [String: Any]
        else { return } // service down / bad payload: keep the last state
        let phase = obj["phase"] as? String ?? "idle"

        // While natively recording, THIS process is the authority — the
        // service is idle then and would report its own (stale) phase.
        guard state != "recording" else { return }

        switch phase {
        case "succeeded", "failed":
            // Already shown this outcome once — the service keeps reporting
            // it until the next operation starts.
            guard lastTerminalPhase != phase else { return }
            // macOS paste ownership: the launchd service has no Aqua
            // session, so its pbcopy/osascript silently miss (verified:
            // pbcopy from a launchd agent no-ops). The service transcribes;
            // THIS app — a GUI process — owns the pasteboard and the Cmd+V.
            if phase == "succeeded", let text = obj["last_result"] as? String, !text.isEmpty {
                pasteLocally(text)
            }
            showTerminal(phase, detail: obj["last_error"] as? String)
            return
        default:
            lastTerminalPhase = nil
        }

        let newState: String
        switch phase {
        case "recording":                    newState = "recording"
        case "transcribing", "pasting":      newState = "transcribing"
        case "translating":                  newState = "translating"
        default:                             newState = "idle"
        }
        // Optimistic hold: right after a deliver, keep the busy icon while
        // the service acknowledges. Prevents a busy→idle flash when this
        // poll races ahead of the round-trip.
        if newState == "idle" && Date() < optimisticUntil { return }
        guard newState != state else { return }
        state = newState
        render()
    }


    /// Put text on the system pasteboard and inject Cmd+V into the
    /// frontmost app. NSPasteboard talks to the pasteboard server directly
    /// from this GUI process — the path the launchd service cannot take.
    /// Cmd+V is posted via CGEvent from THIS process (not osascript): the
    /// synthetic event is attributed to the menubar app, so a single
    /// Accessibility grant suffices. osascript's keystroke is attributed
    /// to osascript itself, which macOS denies by default (-1002) even
    /// when this app is trusted — the Apple-Events→System-Events chain is
    /// the fragile path that silently no-ops.
    private func pasteLocally(_ text: String) {
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(text, forType: .string)
        // VirtualKey: 0x09 = V, 0x37 = left Command. Post a full
        // down→up pair with the command flag set; some apps require the
        // modifier key itself to be held, so drive Cmd explicitly.
        guard let src = CGEventSource(stateID: .hidSystemState) else { return }
        let post: (CGKeyCode, Bool, CGEventFlags?) -> Void = { key, down, flags in
            guard let ev = CGEvent(keyboardEventSource: src, virtualKey: key, keyDown: down) else { return }
            if let flags = flags { ev.flags = flags }
            ev.post(tap: .cgSessionEventTap)
        }
        post(0x37, true, nil)                       // Cmd down
        post(0x09, true, .maskCommand)              // V down
        post(0x09, false, .maskCommand)             // V up
        post(0x37, false, nil)                      // Cmd up
    }

    /// Display a terminal phase (succeeded/failed) once, then fall back to
    /// idle — the service keeps the phase until the next operation.
    private func showTerminal(_ phase: String, detail: String?) {
        lastTerminalPhase = phase
        state = phase
        render()
        if phase == "failed", let detail = detail, !detail.isEmpty {
            notifyUser("失败：" + detail)
        }
        terminalTimer?.invalidate()
        terminalTimer = Timer.scheduledTimer(withTimeInterval: phase == "failed" ? 2.0 : 1.5,
                                             repeats: false) { [weak self] _ in
            guard let self = self, self.state == phase else { return }
            self.state = "idle"
            self.render()
        }
    }

    // ── Actions ───────────────────────────────────────────────────────────

    /// Toggle with in-app native recording (TCC-safe: the mic grant from
    /// requestRecordPermission applies to THIS process's AVAudioEngine —
    /// no bash/ffmpeg attribution chain that macOS silently zero-fills).
    private func nativeToggle(translate: Bool) {
        switch state {
        case "recording":
            recorder.stop { wavURL in
                guard let wavURL = wavURL else {
                    DispatchQueue.main.async {
                        self.state = "idle"
                        self.render()
                    }
                    return
                }
                DispatchQueue.main.async {
                    self.state = translate ? "translating" : "transcribing"
                    // Hold the busy icon across the deliver round-trip; a
                    // status poll racing ahead of it would flash idle.
                    self.optimisticUntil = Date().addingTimeInterval(0.9)
                    self.render()
                }
                // hand the finalized WAV to the service: transcribe→(translate)→paste
                self.deliver(wav: wavURL.path, translate: translate)
            }
        case "transcribing", "translating":
            break // busy — the service's StillTranscribing guard is authoritative
        default:
            // idle, or showing a transient succeeded/failed result
            terminalTimer?.invalidate()
            let ok = recorder.start()
            if ok {
                state = "recording"
                render()
                restartWatchdog(translate: translate)
            } else {
                notifyUser("麦克风不可用 — 系统设置 > 隐私与安全性 > 麦克风，允许本应用")
            }
        }
    }

    /// Hand the WAV to the service via the CLI. deliver is a fast socket
    /// round-trip (the service transcribes asynchronously), so capturing
    /// its JSON response is safe off-main and surfaces validation errors
    /// ("delivered recording is empty") that would otherwise be silent.
    private func deliver(wav: String, translate: Bool) {
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }
            let flag = translate ? " --translate" : ""
            // --no-paste: THIS app owns the pasteboard + Cmd+V (pasteLocally,
            // via CGEvent). Without it the CLI would also poll + paste,
            // racing the menubar's own poll → intermittent double-paste.
            let (out, _) = self.captureStatus("'\(cli)' deliver '\(wav)'\(flag) --no-paste --json 2>/dev/null")
            let obj = (try? JSONSerialization.jsonObject(with: Data(out.utf8))) as? [String: Any]
            DispatchQueue.main.async {
                if obj?["ok"] as? Bool == true {
                    // Accepted — the poll timer now tracks the service phase.
                    self.optimisticUntil = Date().addingTimeInterval(0.9)
                } else {
                    let detail = (obj?["error"] as? [String: Any])?["detail"] as? String ?? "服务未运行"
                    self.showTerminal("failed", detail: nil) // icon only — one notification below
                    self.notifyUser("提交失败：" + detail)
                }
            }
        }
    }

    private var watchdog: Timer?
    private func restartWatchdog(translate: Bool) {
        watchdog?.invalidate()
        watchdog = Timer.scheduledTimer(withTimeInterval: 60, repeats: false) { [weak self] _ in
            guard let self = self, self.state == "recording" else { return }
            self.nativeToggle(translate: translate) // stop + deliver
        }
    }

    @objc func toggle()          { nativeToggle(translate: false) }
    @objc func toggleTranslate() { nativeToggle(translate: true) }
    @objc func cancel() {
        recorder.cancel()
        watchdog?.invalidate()
        fireCli("cancel")
        lastTerminalPhase = nil
        state = "idle"
        render()
    }
    @objc func quit() { exit(0) }

    /// User-facing notification (always async, never blocks the caller).
    private func notifyUser(_ msg: String) {
        let esc = msg.replacingOccurrences(of: "\"", with: "\\\"")
        DispatchQueue.global().async {
            _ = self.capture("osascript -e 'display notification \"\(esc)\" with title \"语音输入\"' 2>/dev/null")
        }
    }

    // Right-click menu actions (bar/SasayakiContextMenu.qml parity).
    @objc func openControlCenter() {
        launchInTerminal(appID: "io.github.iamcheyan.sasayaki", [cli])
    }
    @objc func editConfigFile() {
        // Resolve the editor: skip placeholder values (CI/lab images set
        // EDITOR=true), then fall back through the usual TUI editors.
        // bin/sumika-edit-sasayaki-config parity.
        try? FileManager.default.createDirectory(atPath: (configPath as NSString).deletingLastPathComponent,
                                                 withIntermediateDirectories: true)
        var editor = ""
        let env = ProcessInfo.processInfo.environment
        for cand in [env["VISUAL"] ?? "", env["EDITOR"] ?? "", "nvim", "vim", "vi", "nano"] {
            if cand.isEmpty { continue }
            if ["true", "false", ":", "/bin/true", "/usr/bin/true"].contains(cand) { continue }
            if let exe = cand.split(separator: " ").first, which(String(exe)) != nil {
                editor = cand
                break
            }
        }
        if editor.isEmpty { editor = "vi" }
        launchInTerminal(appID: "io.github.iamcheyan.sasayaki.editconfig", [editor, configPath])
    }
    @objc func serviceRestart() {
        fireCli("service restart")
    }

    // ── Repair ────────────────────────────────────────────────────────────
    // One-shot fix for the failure modes actually seen in the field.
    // `sasayaki repair` handles the service side (venv / model / stale
    // socket / LaunchAgent); permissions and hotkeys belong to THIS
    // process, so they are verified here after the CLI returns.
    @objc func runRepair() {
        if state != "idle" {
            notifyUser("正在录音或处理中 — 请先停止后再修复")
            return
        }
        notifyUser("修复中…")
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            guard let self = self else { return }
            let (_, code) = self.captureStatus("'\(cli)' repair 2>/dev/null")
            DispatchQueue.main.async { self.finishRepair(code: code) }
        }
    }

    private func finishRepair(code: Int32) {
        var failed: [String] = [], needUser: [String] = []
        if code != 0 {
            failed.append("服务端修复未完成（退出码 \(code)，运行 sasayaki repair 查看详情）")
        }

        // Hotkeys: idempotent re-registration fixes a lost registration.
        if !registerHotkeys() { failed.append("全局快捷键注册失败") }

        // Accessibility (auto-paste Cmd+V).
        if !AXIsProcessTrusted() {
            needUser.insert("辅助功能授权（已打开设置）", at: 0)
            NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")!)
        }

        let ok = failed.isEmpty && needUser.isEmpty
        var msg = ok ? "修复完成：一切正常" : "修复完成：" + (needUser + failed).joined(separator: "；")

        // Microphone (TCC) — the Swift-visible `recordPermission` property
        // name collides with its enum type and is unnameable; probe via the
        // request callback instead: it answers immediately with the current
        // state and only prompts when undecided.
        if #available(macOS 14.0, *) {
            AVAudioApplication.requestRecordPermission { granted in
                DispatchQueue.main.async {
                    if !granted {
                        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone")!)
                        msg += "；麦克风需授权（设置已打开）"
                    }
                    self.notifyUser(msg)
                }
            }
        } else {
            notifyUser(msg)
        }
    }
    private var hotkeyRefs: [EventHotKeyRef] = []
    private var hotkeyHandlerInstalled = false

    /// Idempotent: repair re-registers by dropping any previous registration
    /// first (lost registrations after display sleep are a known failure).
    @discardableResult
    private func registerHotkeys() -> Bool {
        for ref in hotkeyRefs { UnregisterEventHotKey(ref) }
        hotkeyRefs.removeAll()

        if !hotkeyHandlerInstalled {
            hotkeyHandlerInstalled = true
            var specs = [EventTypeSpec](
                repeating: EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                                         eventKind: UInt32(kEventHotKeyPressed)),
                count: 1
            )
            let selfPtr = Unmanaged.passRetained(self).toOpaque()
            InstallEventHandler(GetApplicationEventTarget(), { _, event, userData in
                guard let event = event, let userData = userData else { return noErr }
                var hkID = EventHotKeyID()
                GetEventParameter(event, UInt32(kEventParamDirectObject),
                                  UInt32(typeEventHotKeyID), nil,
                                  MemoryLayout<EventHotKeyID>.size, nil, &hkID)
                let menu = Unmanaged<VoiceMenu>.fromOpaque(userData).takeUnretainedValue()
                DispatchQueue.main.async {
                    switch hkID.id {
                    case 1: menu.toggle()
                    case 2: menu.toggleTranslate()
                    default: break
                    }
                }
                return noErr
            }, 1, &specs, selfPtr, nil)
        }

        var ok = true
        var ref: EventHotKeyRef?
        if RegisterEventHotKey(UInt32(kVK_F13), 0, EventHotKeyID(signature: 0x53415359 /* SASY */, id: 1),
                               GetApplicationEventTarget(), 0, &ref) == noErr, let r = ref {
            hotkeyRefs.append(r)
        } else { ok = false }
        ref = nil
        if RegisterEventHotKey(UInt32(kVK_F14), 0, EventHotKeyID(signature: 0x53415359 /* SASY */, id: 2),
                               GetApplicationEventTarget(), 0, &ref) == noErr, let r = ref {
            hotkeyRefs.append(r)
        } else { ok = false }
        return ok
    }

    // ── Launch at login ───────────────────────────────────────────────────

    private var launchAgentPath: String {
        NSHomeDirectory() + "/Library/LaunchAgents/io.github.iamcheyan.sasayaki.menubar.plist"
    }
    private var appBundlePath: String {
        URL(fileURLWithPath: binDir)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .path
    }
    private func launchAtLoginEnabled() -> Bool {
        FileManager.default.fileExists(atPath: launchAgentPath)
    }
    @objc func toggleLaunchAtLogin() {
        let path = launchAgentPath
        if launchAtLoginEnabled() {
            try? FileManager.default.removeItem(atPath: path)
        } else {
            let dir = (path as NSString).deletingLastPathComponent
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            let plist = """
            <?xml version="1.0" encoding="UTF-8"?>
            <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
            <plist version="1.0">
            <dict>
              <key>Label</key><string>io.github.iamcheyan.sasayaki.menubar</string>
              <key>ProgramArguments</key>
              <array>
                <string>/usr/bin/open</string>
                <string>\(appBundlePath)</string>
              </array>
              <key>RunAtLoad</key><true/>
              <key>KeepAlive</key><false/>
            </dict>
            </plist>
            """
            try? plist.write(toFile: path, atomically: true, encoding: .utf8)
        }
        render()
    }

    // ── Microphone permission ─────────────────────────────────────────────
    // Spawned ffmpeg captures all-zero samples when TCC has no microphone
    // grant for this app. Request explicitly once at launch so the prompt
    // actually appears.

    private func requestMicPermission() {
        if #available(macOS 14.0, *) {
            AVAudioApplication.requestRecordPermission { _ in }
        } else {
            let engine = AVAudioEngine()
            let input = engine.inputNode
            input.installTap(onBus: 0, bufferSize: 128, format: input.inputFormat(forBus: 0)) { _, _ in }
            do {
                try engine.start()
                DispatchQueue.global().asyncAfter(deadline: .now() + 0.3) {
                    engine.stop()
                    input.removeTap(onBus: 0)
                }
            } catch {
                input.removeTap(onBus: 0)
            }
        }
    }

    // ── Accessibility (auto-paste) ────────────────────────────────────────
    // The synthetic Cmd+V (paste-at-cursor) is attributed to this app; without
    // an Accessibility grant it silently fails and the text stays on the
    // clipboard. Prompt once — the system dialog offers to open Settings.

    private func checkAccessibility() {
        let opts = ["AXTrustedCheckOptionPrompt": true] as CFDictionary
        if !AXIsProcessTrustedWithOptions(opts) {
            notifyUser("自动粘贴需要辅助功能权限 — 系统设置 > 隐私与安全性 > 辅助功能")
        }
    }

    // ── Render ────────────────────────────────────────────────────────────

    private func render() {
        // Icon policy (state mapping from SasayakiInput.qml):
        //   idle         — white template mic (matches menu bar, dark & light)
        //   recording    — white template waveform, blinking (识别动画)
        //   transcribing — orange waveform, blinking
        //   translating  — blue waveform+globe, blinking
        //   succeeded    — green check, transient 1.5 s
        //   failed       — red warning, transient 2 s
        let icon: String
        let tint: NSColor?
        let template: Bool
        switch state {
        case "recording":    (icon, tint, template) = ("waveform", nil, true)
        case "transcribing": (icon, tint, template) = ("waveform", .systemOrange, false)
        case "translating":  (icon, tint, template) = ("waveform.badge.globe", .systemBlue, false)
        case "succeeded":    (icon, tint, template) = ("checkmark.circle.fill", .systemGreen, false)
        case "failed":       (icon, tint, template) = ("exclamationmark.triangle.fill", .systemRed, false)
        default:             (icon, tint, template) = ("mic", nil, true)
        }
        item.button?.image = NSImage(systemSymbolName: icon, accessibilityDescription: "语音输入")
        item.button?.image?.isTemplate = template
        item.button?.contentTintColor = tint

        // Blink during processing states (recording + transcribing + translating).
        let busy = (state == "recording" || state == "transcribing" || state == "translating")
        if busy {
            if blinkTimer == nil {
                blinkOn = true
                item.button?.alphaValue = 1.0
                blinkTimer = Timer.scheduledTimer(withTimeInterval: 0.35, repeats: true) { [weak self] _ in
                    guard let self = self else { return }
                    self.blinkOn.toggle()
                    self.item.button?.alphaValue = self.blinkOn ? 1.0 : 0.25
                }
            }
        } else {
            blinkTimer?.invalidate()
            blinkTimer = nil
            item.button?.alphaValue = 1.0
        }

        // Menu: recording stays toggleable (press to stop); only the
        // downstream processing states (transcribing/translating) lock it.
        let processing = (state == "transcribing" || state == "translating")
        toggleItem?.title = processing ? "处理中…" : (state == "recording" ? "停止并转写" : "开始录音 (F13)")
        toggleItem?.isEnabled = !processing
        cancelItem?.isEnabled = (state == "recording" || processing)

        let on = launchAtLoginEnabled()
        launchItem?.state = on ? .on : .off
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory) // no Dock icon, menu bar only
let delegate = AppDelegate()
app.delegate = delegate
app.run()

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var menu: VoiceMenu!
    func applicationDidFinishLaunching(_ n: Notification) {
        menu = VoiceMenu()
    }
}
