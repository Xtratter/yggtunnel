import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material

// A capsule that appears at the top of the window on state changes: fades in and scales
// from 0.8 to 1 with an overshoot, then hides after a while unless it is "sticky".
Rectangle {
    id: island
    objectName: "island"

    property string text: ""
    property bool sticky: false
    property bool shown: false
    property int hideDelay: 3000
    property string mode: ""   // lets the owner keep updating the text of a particular message

    implicitWidth: label.implicitWidth + 40
    implicitHeight: 40
    radius: height / 2
    color: Material.dialogColor
    border.width: 1
    border.color: Qt.alpha(Material.foreground, 0.12)

    opacity: shown ? 1 : 0
    scale: shown ? 1 : 0.8
    visible: opacity > 0

    Behavior on opacity { NumberAnimation { duration: 200 } }
    Behavior on scale { NumberAnimation { duration: 250; easing.type: Easing.OutBack; easing.overshoot: 1.6 } }

    // A sticky message (connecting, reconnecting) stays until the owner hides it.
    function show(message, keep, kind) {
        text = message
        sticky = keep
        mode = kind || ""
        shown = true
        if (keep)
            hideTimer.stop()
        else
            hideTimer.restart()
    }

    Timer {
        id: hideTimer
        interval: island.hideDelay
        running: island.shown && !island.sticky
        onTriggered: island.shown = false
    }

    Label {
        id: label
        objectName: "islandLabel"
        anchors.centerIn: parent
        text: island.text
        font.pixelSize: 15
    }
}
