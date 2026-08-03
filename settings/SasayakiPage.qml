import qs
import qs.modules.common
import qs.modules.common.widgets
import qs.modules.common.functions
import qs.modules.settings
import qs.modules.settings.widgets
import qs.modules.sasayaki
import QtQuick
import QtQuick.Layouts
import Quickshell

PageBody {
    id: pageRoot
    property var settingsRoot: null

    // Status card
    SettingsCard {
        title: "Sasayaki Voice Input"
        subtitle: "Local speech-to-text with optional translation"

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 8

            SettingsRow {
                label: "Service"
                SettingsStatusPill {
                    text: SasayakiInput.serviceState === "running" ? "Running" : "Stopped"
                    positive: SasayakiInput.serviceState === "running"
                }
            }
            SettingsRow {
                label: "Model"
                SettingsStatusPill {
                    text: SasayakiInput.model ? SasayakiInput.speechModel : "Missing"
                    positive: SasayakiInput.model
                }
            }
            SettingsRow {
                label: "Worker"
                SettingsStatusPill {
                    text: SasayakiInput.workerWarm ? "Warm" : "Standby"
                    positive: SasayakiInput.workerWarm
                }
            }
            SettingsRow {
                label: "Microphone"
                SettingsStatusPill {
                    text: SasayakiInput.microphone ? "Ready" : "Unavailable"
                    positive: SasayakiInput.microphone
                }
            }
            SettingsRow {
                label: "Paste"
                SettingsStatusPill {
                    text: SasayakiInput.pasteReady
                        ? SasayakiInput.pasteBackend
                        : "Unavailable"
                    positive: SasayakiInput.pasteReady
                }
            }
            SettingsRow {
                label: "Translation"
                SettingsStatusPill {
                    text: SasayakiInput.translation === "ready" ? "Ready" : "Off"
                    positive: SasayakiInput.translation === "ready"
                }
            }
        }
    }

    // Actions card
    SettingsCard {
        title: "Actions"

        ButtonRow {
            SettingsButton {
                label: "Open Control Center"
                iconName: "keyboard_voice"
                onClicked: {
                    if (settingsRoot) settingsRoot.dismiss();
                    Quickshell.execDetached(["sasayaki"]);
                }
            }
            SettingsButton {
                label: "Repair Everything"
                iconName: "build"
                onClicked: SasayakiInput.repair()
            }
            SettingsButton {
                label: "Restart Service"
                iconName: "refresh"
                onClicked: Quickshell.execDetached(["sasayaki", "service", "restart"])
            }
            SettingsButton {
                label: "View Logs"
                iconName: "terminal"
                onClicked: {
                    if (settingsRoot) settingsRoot.dismiss();
                    Quickshell.execDetached(["bash", "-c",
                        "sasayaki logs"]);
                }
            }
        }
    }

    // Config card
    SettingsCard {
        title: "Configuration"

        ColumnLayout {
            Layout.fillWidth: true
            spacing: 6

            SettingsRow {
                label: "Config file"
                SettingsSummary {
                    text: "~/.config/sasayaki/config.json"
                }
            }
            SettingsRow {
                label: "Keybindings"
                SettingsSummary {
                    text: SasayakiInput.state !== "setup"
                        ? "Managed by `sasayaki bindings`"
                        : "Set up first"
                }
            }

            ButtonRow {
                SettingsButton {
                    label: "Edit Configuration"
                    iconName: "edit"
                    onClicked: {
                        Quickshell.execDetached(["bash", "-c",
                            "editor=${EDITOR:-nano}; $editor ~/.config/sasayaki/config.json"]);
                    }
                }
                SettingsButton {
                    label: "Diagnose"
                    iconName: "medical_information"
                    onClicked: {
                        if (settingsRoot) settingsRoot.dismiss();
                        Quickshell.execDetached(["bash", "-c",
                            "sasayaki diagnose; read -p 'Press Enter...'"]);
                    }
                }
            }
        }
    }
}