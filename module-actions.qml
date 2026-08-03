import QtQuick

import qs.modules.sasayaki
import qs.core.runtime

/// Sasayaki voice input action registrations.
///
/// Registers QML-callback actions for toggling/cancelling voice input
/// via the SasayakiInput service. Loaded by ModuleActionHost when the
/// sasayaki module is enabled.
Item {
    Component.onCompleted: {
        ActionManager.register("sasayaki.toggle", "sasayaki", "Toggle voice input", {
            type: "qml",
            call: function(p) { SasayakiInput.toggle() }
        }, {description: "Start or stop voice input recording"})

        ActionManager.register("sasayaki.cancel", "sasayaki", "Cancel voice input", {
            type: "qml",
            call: function(p) { SasayakiInput.cancel() }
        }, {description: "Cancel active voice input"})

        ActionManager.register("sasayaki.translate-toggle", "sasayaki", "Toggle translated voice input", {
            type: "qml",
            call: function(p) { SasayakiInput.toggleTranslation() }
        }, {description: "Record speech, translate it online, and paste the result"})

        ActionManager.register("sasayaki.repair", "sasayaki", "Repair Sasayaki", {
            type: "qml",
            call: function(p) { SasayakiInput.repair() }
        }, {description: "Repair runtime, model, service and desktop integration"})
    }
}