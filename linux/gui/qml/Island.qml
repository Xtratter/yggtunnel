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

    function show(message, keep) {
        text = message
        sticky = keep
        shown = true
        hideTimer.restart()
    }

    Timer {
        id: hideTimer
        interval: 3000
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
