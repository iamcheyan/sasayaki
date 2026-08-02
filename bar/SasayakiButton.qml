import qs
import qs.modules.sasayaki
import qs.modules.bar
import qs.modules.common
import qs.modules.common.widgets
import QtQuick
import Quickshell
import QtQuick.Layouts

Item {
    id: root

    implicitWidth: Config.options.bar.rightIconSlotWidth
    implicitHeight: Config.options.bar.rightIconSlotWidth
    Layout.fillHeight: true

    readonly property string voiceState: SasayakiInput.state
    readonly property bool isRecording: voiceState === "recording"
    readonly property bool isTranscribing: voiceState === "transcribing"
    readonly property bool isTranslating: voiceState === "translating"
    readonly property bool isSetup: voiceState === "setup"
    readonly property bool isError: voiceState === "error"
    readonly property bool isActive: isRecording || isTranscribing || isTranslating || isSetup

    readonly property color colorIdle: Appearance.colors.colBarText
    readonly property color colorRecording: "#F5C542"
    readonly property color colorTranscribing: "#5B9BD5"
    readonly property color colorError: "#FF3B30"

    readonly property color iconColor: {
        if (root.isError) return root.colorError
        if (root.isRecording) return root.colorRecording
        if (root.isTranscribing || root.isTranslating) return root.colorTranscribing
        if (root.isSetup) return root.colorRecording
        return root.colorIdle
    }

    readonly property string iconText: {
        if (root.isTranscribing || root.isTranslating) return NerdIconMap.hourglass
        if (root.isActive) return NerdIconMap.mic
        return NerdIconMap.mic
    }

    RippleButton {
        id: button
        anchors.centerIn: parent
        width: Config.options.bar.rightIconSlotWidth
        height: Config.options.bar.rightIconSlotWidth
        buttonRadius: Config.options.bar.rightIconSlotWidth / 2
        colBackground: "transparent"
        colBackgroundHover: Qt.rgba(1, 1, 1, 0.10)
        colBackgroundToggled: Qt.rgba(1, 1, 1, 0.18)
        colBackgroundToggledHover: Qt.rgba(1, 1, 1, 0.26)
        colRipple: Qt.rgba(1, 1, 1, 0.12)
        colRippleToggled: Qt.rgba(1, 1, 1, 0.18)
        toggled: GlobalStates.barPopupType === "sasayaki"

        onClicked: {
            if (Date.now() - GlobalStates.barPopupDismissedAt < 200)
                return;
            SasayakiInput.toggle();
            GlobalStates.barPopupType = GlobalStates.barPopupType === "sasayaki"
                ? ""
                : "sasayaki";
        }

        // Right click: show context menu
        altAction: function(event) {
            menuLoader.open();
        }
    }

    // Pulse ring for recording/transcribing
    Rectangle {
        id: pulseRing
        anchors.centerIn: button
        width: button.width
        height: button.height
        radius: width / 2
        color: "transparent"
        border.width: 2
        border.color: root.isRecording ? root.colorRecording
            : root.isTranscribing || root.isTranslating ? root.colorTranscribing
            : root.colorRecording
        visible: root.isActive
        opacity: 0.75

        SequentialAnimation on scale {
            running: root.isRecording
            loops: Animation.Infinite
            NumberAnimation { from: 1.0; to: 1.65; duration: 750; easing.type: Easing.OutCubic }
            NumberAnimation { from: 1.65; to: 1.0; duration: 0 }
        }
        SequentialAnimation on opacity {
            running: root.isRecording || root.isTranscribing || root.isTranslating
            loops: Animation.Infinite
            NumberAnimation { to: 0.8; duration: 600; easing.type: Easing.InOutQuad }
            NumberAnimation { to: 0.1; duration: 600; easing.type: Easing.InOutQuad }
        }
    }

    BarNerdIcon {
        id: icon
        anchors.centerIn: button
        iconSize: root.isTranscribing || root.isTranslating
            ? Config.options.bar.rightIconSize * 0.72
            : Config.options.bar.rightIconSize
        text: root.iconText
        color: root.iconColor

        Behavior on color { ColorAnimation { duration: 120 } }
    }

    // Recording blink animation
    SequentialAnimation {
        id: recordingBlink
        loops: 3
        PropertyAnimation {
            target: icon
            property: "rotation"
            to: 15
            duration: 50
        }
        PropertyAnimation {
            target: icon
            property: "rotation"
            to: -15
            duration: 50
        }
        PropertyAnimation {
            target: icon
            property: "rotation"
            to: 0
            duration: 50
        }
    }

    // Transcribing rotation animation
    SequentialAnimation {
        id: rotateAnim
        running: root.isTranscribing || root.isTranslating
        loops: Animation.Infinite
        NumberAnimation {
            target: icon
            property: "rotation"
            from: 0
            to: 180
            duration: 2000
            easing.type: Easing.InOutQuad
        }
        PauseAnimation { duration: 300 }
        PropertyAction {
            target: icon
            property: "rotation"
            value: 0
        }
        onStopped: icon.rotation = 0
    }

    onIsErrorChanged: {
        if (root.isError)
            recordingBlink.start();
    }
    BarContextMenu {
        id: menuLoader
        anchorItem: button
        sourceComponent: SasayakiContextMenu {}
    }
}