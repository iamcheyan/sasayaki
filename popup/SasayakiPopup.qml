import qs
import qs.services
import qs.modules.bar
import qs.modules.common
import qs.modules.sasayaki
import qs.modules.common.widgets
import qs.modules.common.functions
import QtQuick
import QtQuick.Layouts
import Quickshell

PopupColumn {
    id: voicePanel

    function stateLabel() {
        if (SasayakiInput.state === "setup") return "Not Installed"
        if (SasayakiInput.state === "idle") return "Ready"
        if (SasayakiInput.state === "recording") return "Recording"
        if (SasayakiInput.state === "transcribing") return "Transcribing"
        if (SasayakiInput.state === "translating") return "Translating"
        if (SasayakiInput.state === "success") return "Transcription Success"
        if (SasayakiInput.state === "error") return "Error"
        return SasayakiInput.state
    }
    function tone() {
        if (SasayakiInput.state === "idle" || SasayakiInput.state === "success") return TuiStyle.success
        if (SasayakiInput.state === "recording" || SasayakiInput.state === "error") return TuiStyle.danger
        if (SasayakiInput.state === "transcribing" || SasayakiInput.state === "translating" || SasayakiInput.state === "setup") return TuiStyle.warning
        return TuiStyle.muted
    }

    PopupHeader {
        Layout.fillWidth: true
        icon: NerdIconMap.mic
        title: "Voice Input"
        subtitle: voicePanel.stateLabel()
        tone: voicePanel.tone()
    }

    // Model status card
    Rectangle {
        Layout.fillWidth: true
        Layout.preferredHeight: modelCol.implicitHeight + 16
        color: TuiStyle.panel
        radius: TuiStyle.radius
        clip: true

        ColumnLayout {
            id: modelCol
            anchors.fill: parent
            anchors.margins: 8
            spacing: 4

            PopupInfoRow {
                label: "Model"
                value: SasayakiInput.speechModel.length > 0
                    ? SasayakiInput.speechModel
                    : (SasayakiInput.model ? "Ready" : "Missing")
                valueColor: SasayakiInput.model ? TuiStyle.success : TuiStyle.danger
                showDivider: true
            }

            PopupInfoRow {
                label: "Service"
                value: SasayakiInput.serviceState === "running" ? "Active" : "Stopped"
                valueColor: SasayakiInput.serviceState === "running" ? TuiStyle.success : TuiStyle.danger
                showDivider: true
            }

            PopupInfoRow {
                label: "Worker"
                value: SasayakiInput.workerWarm ? "Warm (RAM Loaded)" : "Standby"
                valueColor: SasayakiInput.workerWarm ? TuiStyle.success : TuiStyle.dim
                showDivider: true
            }

            PopupInfoRow {
                label: "Microphone"
                value: SasayakiInput.microphone ? "Ready" : "Unavailable"
                valueColor: SasayakiInput.microphone ? TuiStyle.success : TuiStyle.danger
                showDivider: true
            }

            PopupInfoRow {
                label: "Paste"
                value: SasayakiInput.pasteReady
                    ? "Ready (" + SasayakiInput.pasteBackend + ")"
                    : "Unavailable"
                valueColor: SasayakiInput.pasteReady ? TuiStyle.success : TuiStyle.danger
                showDivider: false
            }
        }
    }

    // Recording / result section
    Rectangle {
        Layout.fillWidth: true
        Layout.preferredHeight: debugCol.implicitHeight + 16
        color: TuiStyle.panel
        radius: TuiStyle.radius
        clip: true
        visible: SasayakiInput.state !== "setup"

        ColumnLayout {
            id: debugCol
            anchors.fill: parent
            anchors.margins: 8
            spacing: 8

            RowLayout {
                Layout.fillWidth: true
                spacing: 12

                // Circle button for record toggle
                Rectangle {
                    id: debugRecBtn
                    Layout.preferredWidth: 48
                    Layout.preferredHeight: 48
                    radius: 24
                    color: SasayakiInput.state === "recording" ? TuiStyle.danger : recMouse.containsMouse ? TuiStyle.surfaceHover : TuiStyle.surfaceRaised
                    border.width: 1
                    border.color: SasayakiInput.state === "recording" ? TuiStyle.danger : TuiStyle.line

                    NerdIcon {
                        anchors.centerIn: parent
                        iconSize: 20
                        text: SasayakiInput.state === "recording" ? NerdIconMap.stop : NerdIconMap.mic
                        color: TuiStyle.fg
                    }

                    MouseArea {
                        id: recMouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        enabled: SasayakiInput.state === "idle" || SasayakiInput.state === "recording"
                        onClicked: SasayakiInput.toggle()
                    }
                }

                ColumnLayout {
                    Layout.fillWidth: true
                    spacing: 2

                    StyledText {
                        text: {
                            if (SasayakiInput.state === "recording") return `Recording ${SasayakiInput.recordingDuration.toFixed(1)}s`
                            if (SasayakiInput.state === "transcribing") return "Transcribing…"
                            if (SasayakiInput.state === "translating") return "Translating…"
                            if (SasayakiInput.state === "success") return "Transcription ready"
                            if (SasayakiInput.state === "error") return "Error"
                            return "Tap mic to start"
                        }
                        font.family: Appearance.font.family.main
                        font.pixelSize: Appearance.font.pixelSize.small
                        font.weight: Font.DemiBold
                        color: voicePanel.tone()
                    }

                    StyledText {
                        Layout.fillWidth: true
                        text: SasayakiInput.lastError || SasayakiInput.lastTranscription || "—"
                        wrapMode: Text.Wrap
                        maximumLineCount: 2
                        elide: Text.ElideRight
                        font.family: Appearance.font.family.main
                        font.pixelSize: Appearance.font.pixelSize.smaller
                        color: SasayakiInput.lastError ? TuiStyle.danger : TuiStyle.fg
                    }
                }
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 8

                PopupIconButton {
                    label: "COPY TEXT"
                    enabledState: SasayakiInput.lastTranscription.length > 0
                    onClicked: {
                        Quickshell.execDetached(["bash", "-c",
                            `printf '%s' '${StringUtils.shellSingleQuoteEscape(SasayakiInput.lastTranscription)}' | wl-copy`]);
                        SasayakiInput.notify("Copied", SasayakiInput.lastTranscription, "edit-copy");
                    }
                }
                PopupIconButton {
                    label: "CANCEL"
                    enabledState: SasayakiInput.state === "recording"
                    onClicked: SasayakiInput.cancel()
                }
            }
        }
    }

    // History section
    Rectangle {
        Layout.fillWidth: true
        Layout.preferredHeight: Math.min(historyList.implicitHeight + 16, 160)
        color: TuiStyle.panel
        radius: TuiStyle.radius
        clip: true
        visible: SasayakiInput.lastTranscription.length > 0

        ColumnLayout {
            id: historyList
            anchors.fill: parent
            anchors.margins: 8
            spacing: 4

            StyledText {
                text: "Last Result"
                font.family: Appearance.font.family.monospace
                font.pixelSize: Appearance.font.pixelSize.smaller
                font.weight: Font.Bold
                color: TuiStyle.dim
            }

            StyledText {
                Layout.fillWidth: true
                text: SasayakiInput.lastTranscription
                wrapMode: Text.Wrap
                maximumLineCount: 4
                elide: Text.ElideRight
                font.family: Appearance.font.family.main
                font.pixelSize: Appearance.font.pixelSize.small
                color: TuiStyle.fg
            }
        }
    }

    RowLayout {
        Layout.fillWidth: true
        spacing: 8

        PopupIconButton {
            label: SasayakiInput.state === "setup" ? "Setup" : "Test"
            onClicked: {
                if (SasayakiInput.state === "setup") {
                    SasayakiInput.setup();
                } else {
                    SasayakiInput.toggle();
                }
                GlobalStates.barPopupType = "";
            }
        }
        PopupIconButton {
            label: "Check State"
            onClicked: SasayakiInput.refresh()
        }
        PopupIconButton {
            label: "Control Center"
            onClicked: {
                Quickshell.execDetached(["sasayaki"]);
                GlobalStates.barPopupType = "";
            }
        }
    }

    PopupFooterLink {
        Layout.fillWidth: true
        text: "Open settings"
        onClicked: {
            GlobalStates.barPopupType = "";
            Quickshell.execDetached(["sumika-settings", "open", "sasayaki"]);
        }
    }
}