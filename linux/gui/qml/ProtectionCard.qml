import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

// The kill switch and its local-network exception. The status line repeats only what the daemon reports.
Pane {
    id: card
    objectName: "protectionCard"

    property bool killSwitch: false
    property bool allowLan: true
    property bool active: false
    property bool connected: false
    property bool busy: false

    signal killSwitchRequested(bool on)
    signal allowLanRequested(bool allow)

    padding: 20
    background: Rectangle {
        radius: 20
        color: Material.dialogColor
        border.width: 1
        border.color: Qt.alpha(Material.foreground, 0.1)
    }

    ColumnLayout {
        anchors.left: parent.left
        anchors.right: parent.right
        spacing: 8

        RowLayout {
            Layout.fillWidth: true
            Label {
                Layout.fillWidth: true
                text: qsTr("Protection")
                font.pixelSize: 18
                font.weight: Font.Medium
            }
            Label {
                objectName: "protectionStatus"
                text: card.active ? qsTr("Active")
                      : !card.killSwitch ? qsTr("Off")
                      : card.connected ? qsTr("Not active")
                      : qsTr("Armed: it starts with the next connection")
                horizontalAlignment: Text.AlignRight
                elide: Text.ElideLeft
                Layout.maximumWidth: card.width * 0.55
                color: card.active ? Material.color(Material.Green) : Material.foreground
                opacity: card.active ? 1 : 0.7
            }
        }

        Switch {
            id: ks
            objectName: "killSwitchSwitch"
            Layout.fillWidth: true
            enabled: !card.busy
            text: qsTr("Kill switch")
            checked: card.killSwitch
            onClicked: {
                var want = checked
                checked = Qt.binding(function () { return card.killSwitch }) // the daemon decides what is shown
                card.killSwitchRequested(want)
            }
        }
        Label {
            Layout.fillWidth: true
            Layout.leftMargin: 8
            wrapMode: Text.WordWrap
            opacity: 0.7
            text: qsTr("Traffic that is not going through the tunnel is dropped while you are connected.")
        }

        Switch {
            id: lan
            objectName: "lanSwitch"
            Layout.fillWidth: true
            enabled: card.killSwitch && !card.busy
            text: qsTr("Allow local network")
            checked: card.allowLan
            onClicked: {
                var want = checked
                checked = Qt.binding(function () { return card.allowLan })
                card.allowLanRequested(want)
            }
        }
    }
}
