pragma Singleton
pragma ComponentBehavior: Bound

import qs.core.runtime
import qs
import qs.modules.common
import qs.modules.common.functions
import QtQuick
import Quickshell
import Quickshell.Io

Singleton {
    id: root

    // ── State (mirrors sasayaki status --json) ──
    property string state: "init"
    property string phase: "idle"
    property string serviceState: "stopped"
    property bool runtime: false
    property bool model: false
    property bool microphone: false
    property bool pasteReady: false
    property string pasteBackend: ""
    property string speechModel: ""
    property string language: ""
    property string translation: ""
    property int micLevel: 0
    property int modelSizeMB: 0
    property bool daemonRunning: false
    property bool workerWarm: false

    // ── Last result ──
    property string lastTranscription: ""
    property string lastError: ""

    // ── Translation ──
    property bool translationReady: false
    property string translationTargetLanguage: ""

    // ── History ──
    property list<var> history: []
    readonly property int maxHistory: 20

    // ── Recording ──
    property real recordingDuration: 0
    readonly property real maxRecordingDuration: 90.0

    // Timestamp of the last optimistic recording entry. The bar icon turns
    // yellow the instant the key/button is pressed; polling confirms the real
    // server phase shortly after. This window lets us hold the optimistic
    // state while the server acknowledges, so the icon never flashes idle.
    property real optimisticAt: 0
    readonly property real optimisticHoldMs: 600

    readonly property string binary: {
        const p = Quickshell.env("SASAYAKI_BIN")
        if (p) return p
        const home = FileUtils.trimFileProtocol(Directories.home)
        return home + "/.local/bin/sasayaki"
    }

    // ── State mapping (phase → legacy state names for bar button) ──
    readonly property bool isRecording: state === "recording"
    readonly property bool isTranscribing: state === "transcribing"
    readonly property bool isTranslating: state === "translating"
    readonly property bool isSetup: state === "setup"
    readonly property bool isError: state === "error"
    readonly property bool isActive: isRecording || isTranscribing || isTranslating || isSetup

    Component.onCompleted: {
        if (ModuleLoader.isEnabled("sasayaki")) {
            root.refresh()
        } else {
            console.log("[SasayakiInput] sasayaki module disabled, skipping init")
        }
    }

    // ── Escape binding for cancel during recording ──
    onStateChanged: {
        if (state === "recording") {
            Quickshell.execDetached(["hyprctl", "eval",
                "o.bind(\"escape\", \"Cancel sasayaki recording\", \"sasayaki cancel\")"])
        } else {
            Quickshell.execDetached(["hyprctl", "eval", "hl.unbind(\"escape\")"])
        }
    }

    // ── Polling timer ──
    Timer {
        id: pollTimer
        interval: root.isActive ? 200 : 1000
        repeat: true
        running: ModuleLoader.isEnabled("sasayaki")
        onTriggered: root.refresh()
    }

    // ── Recording duration timer ──
    Timer {
        id: recordingTimer
        interval: 100
        repeat: true
        running: root.isRecording
        onTriggered: {
            root.recordingDuration += 0.1
            if (root.recordingDuration >= root.maxRecordingDuration) {
                root.toggle()
            }
        }
    }

    // ── Status refresh ──
    function refresh() {
        statusProc.running = true
    }

    Process {
        id: statusProc
        command: [root.binary, "status", "--json"]
        running: false
        stdout: StdioCollector {
            onStreamFinished: {
                try {
                    var s = JSON.parse(this.text.trim())
                    root.serviceState = s.service || "stopped"
                    root.phase = s.phase || "idle"
                    root.runtime = s.runtime || false
                    root.model = s.model || false
                    root.microphone = s.microphone || false
                    root.pasteReady = s.paste || false
                    root.pasteBackend = s.paste_backend || ""
                    root.speechModel = s.speech_model || ""
                    root.language = s.language || ""
                    root.translation = s.translation || ""
                    root.micLevel = s.mic_level || 0
                    root.workerWarm = s.worker === "warm"
                    root.lastTranscription = s.last_result || ""
                    root.lastError = s.last_error || ""
                    root.translationReady = s.translation === "ready"

                    // Map to state
                    if (root.serviceState !== "running") {
                        root.state = "setup"
                    } else if (!root.runtime || !root.model) {
                        root.state = "setup"
                    } else if (root.phase === "recording") {
                        root.state = "recording"
                    } else if (root.phase === "transcribing" || root.phase === "pasting") {
                        root.state = "transcribing"
                    } else if (root.phase === "translating") {
                        root.state = "translating"
                    } else if (root.phase === "succeeded") {
                        root.state = "success"
                        successResetTimer.restart()
                    } else if (root.phase === "failed") {
                        root.state = "error"
                        errorResetTimer.restart()
                    } else {
                        root.state = "idle"
                    }

                    // Optimistic hold: right after a press, keep the icon
                    // recording while the server acknowledges the toggle.
                    // Prevents a yellow→idle flash when the status poll
                    // races ahead of the recording actually starting.
                    if (root.state !== "recording"
                            && Date.now() - root.optimisticAt < root.optimisticHoldMs) {
                        root.state = "recording"
                    }
                } catch (e) {
                    root.state = "setup"
                }
            }
        }
    }

    Timer {
        id: successResetTimer
        interval: 1500
        repeat: false
        running: false
        onTriggered: {
            if (root.state === "success") root.state = "idle"
        }
    }

    Timer {
        id: errorResetTimer
        interval: 2000
        repeat: false
        running: false
        onTriggered: {
            if (root.state === "error") root.state = "idle"
        }
    }

    // ── Actions ──
    function toggle() {
        if (state === "setup") {
            root.setup()
            return
        }
        if (state === "error") {
            root.refresh()
            return
        }
        root.recordingDuration = 0
        // Optimistic: switch to recording before the server round-trip so
        // the bar icon reacts instantly. Polling corrects within ~200ms.
        if (root.state !== "recording") {
            root.optimisticAt = Date.now()
            root.state = "recording"
        }
        Quickshell.execDetached([root.binary, "toggle"])
    }

    function cancel() {
        Quickshell.execDetached([root.binary, "cancel"])
    }

    function toggleTranslation() {
        if (state === "setup") {
            root.setup()
            return
        }
        root.recordingDuration = 0
        if (root.state !== "recording") {
            root.optimisticAt = Date.now()
            root.state = "recording"
        }
        Quickshell.execDetached([root.binary, "translate-toggle"])
    }

    // ── Setup ──
    function setup() {
        root.notify("⬇️ Setting up Sasayaki", "Installing runtime and model…")
        setupProc.running = true
    }

    Process {
        id: setupProc
        command: [root.binary, "setup"]
        stdout: SplitParser {
            onRead: (line) => {
                if (line.startsWith("ERROR")) {
                    root.lastError = line
                    root.state = "error"
                }
            }
        }
        onExited: (code, status) => {
            if (code === 0) {
                root.notify("✅ Sasayaki ready", "Voice input is set up")
                root.refresh()
            } else {
                root.lastError = "Setup failed (code " + code + ")"
                root.state = "error"
            }
        }
    }

    // ── Notification helper ──
    function notify(title, body) {
        var args = ["notify-send", "-a", "Sasayaki Voice", "-t", "3000"]
        args.push(title, body)
        Quickshell.execDetached(args)
    }

    // ── Open settings panel ──
    function openSettings() {
        GlobalStates.barPopupType = "sasayaki"
    }

    // ── History ──
    function addToHistory(text) {
        if (!text || text.length === 0) return
        var now = new Date()
        var timeStr = now.getHours().toString().padStart(2, '0') + ":" +
                      now.getMinutes().toString().padStart(2, '0')
        var entry = { text: text, time: timeStr }
        var newHistory = [entry].concat(root.history)
        if (newHistory.length > root.maxHistory) {
            newHistory = newHistory.slice(0, root.maxHistory)
        }
        root.history = newHistory
    }

    function clearHistory() {
        root.history = []
    }
}