pragma ComponentBehavior: Bound
import qs
import qs.modules.common
import qs.modules.sasayaki
import qs.modules.common.widgets
import QtQuick
import Quickshell
import Quickshell.Io

ContextMenuWindow {
    onVisibleChanged: if (visible) wakeStatus.running = true
    Component.onCompleted: wakeStatus.running = true
    id: root

    ContextMenuItem {
        nerdIcon: NerdIconMap.cpu
        labelText: "Control Center (TUI)"
        onClicked: {
            // Launch through sumika-launch-tui so the terminal gets the
            // io.github.iamcheyan.sasayaki app-id — the Hyprland float rule
            // (centered 1180x760) matches that class. Bare `sasayaki`
            // spawns a terminal with default class and the rule never
            // matches.
            Quickshell.execDetached(["sumika-launch-tui",
                "-a", "io.github.iamcheyan.sasayaki", "sasayaki"]);
            root.close();
        }
    }

    ContextMenuItem {
        nerdIcon: NerdIconMap.wrench
        labelText: "Repair Everything"
        onClicked: {
            SasayakiInput.repair();
            root.close();
        }
    }

    ContextMenuSeparator {}

    ContextMenuItem {
        nerdIcon: NerdIconMap.keyboard
        labelText: "Edit Configuration"
        onClicked: {
            // execDetached has no TTY — a TUI editor spawned directly dies
            // instantly and the item looks dead. Go through the extension's
            // launcher script (sumika-launch-tui inside).
            Quickshell.execDetached(["sumika-edit-sasayaki-config"]);
            root.close();
        }
    }

    ContextMenuItem {
        nerdIcon: NerdIconMap.keyboardSettings
        labelText: "Wake CapsLock: " + (root.wakeState.capslock === true ? "ON" : "OFF")
        onClicked: {
            // `toggle` flips server-side state, so a stale status read can
            // never send the wrong direction. The CLI persists and reloads
            // the Hyprland binds + keyd overload.
            wakeToggleProc.command = ["sasayaki", "wake", "capslock", "toggle"];
            wakeToggleProc.running = true;
        }
    }

    ContextMenuItem {
        nerdIcon: NerdIconMap.keyboardSettings
        labelText: "Wake LeftCtrl: " + (root.wakeState.leftctrl === true ? "ON" : "OFF")
        onClicked: {
            wakeToggleProc.command = ["sasayaki", "wake", "leftctrl", "toggle"];
            wakeToggleProc.running = true;
        }
    }

    ContextMenuItem {
        nerdIcon: NerdIconMap.keyboardSettings
        labelText: "Wake RightCtrl: " + (root.wakeState.rightctrl === true ? "ON" : "OFF")
        onClicked: {
            wakeToggleProc.command = ["sasayaki", "wake", "rightctrl", "toggle"];
            wakeToggleProc.running = true;
        }
    }

    // One status call for the whole matrix: `sasayaki wake status` prints
    // "<key> <on|off>" per line.
    property var wakeState: ({})

    Process {
        id: wakeStatus
        command: ["sasayaki", "wake", "status"]
        stdout: StdioCollector {
            onStreamFinished: {
                const state = {}
                const lines = text.trim().split("\n")
                for (let i = 0; i < lines.length; i++) {
                    const parts = lines[i].trim().split(/\s+/)
                    if (parts.length === 2)
                        state[parts[0]] = parts[1] === "on"
                }
                root.wakeState = state
            }
        }
    }

    Process {
        id: wakeToggleProc
        stdout: StdioCollector {
            onStreamFinished: wakeStatus.running = true
        }
    }

    ContextMenuSeparator {}

    ContextMenuItem {
        nerdIcon: NerdIconMap.refresh
        labelText: "Service Restart"
        onClicked: {
            Quickshell.execDetached(["sasayaki", "service", "restart"]);
            root.close();
        }
    }

    ContextMenuItem {
        nerdIcon: NerdIconMap.favorite
        labelText: "Diagnose"
        onClicked: {
            Quickshell.execDetached(["sumika-launch-tui",
                "-a", "io.github.iamcheyan.sumika.sasayakidiagnose",
                "bash", "-c", "sasayaki diagnose; echo; read -r -p 'Press Enter to close...' _"]);
            root.close();
        }
    }
}