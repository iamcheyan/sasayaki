pragma ComponentBehavior: Bound
import qs
import qs.modules.common
import qs.modules.common.widgets
import QtQuick
import Quickshell

ContextMenuWindow {
    id: root

    ContextMenuItem {
        nerdIcon: NerdIconMap.cpu
        labelText: "Control Center (TUI)"
        onClicked: {
            // Plain `sasayaki` opens the TUI in a terminal emulator on its
            // own, so this works on any desktop, not just Sumika.
            Quickshell.execDetached(["sasayaki"]);
            root.close();
        }
    }

    ContextMenuSeparator {}

    ContextMenuItem {
        nerdIcon: NerdIconMap.keyboard
        labelText: "Edit Configuration"
        onClicked: {
            Quickshell.execDetached(["bash", "-c",
                "editor=${EDITOR:-nano}; $editor ~/.config/sasayaki/config.json"]);
            root.close();
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
        nerdIcon: NerdIconMap.stethoscope
        labelText: "Diagnose"
        onClicked: {
            Quickshell.execDetached(["bash", "-c",
                "sasayaki diagnose; read -p 'Press Enter...'"]);
            root.close();
        }
    }
}