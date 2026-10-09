import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

// The state of the connection and the buttons that change it.
Pane {
    id: card

    property string state: "unreachable"
    property string statusText: ""
    property bool reachable: false
    property bool busy: false
    property bool hasProfile: false
    property string profileName: ""
    property string serverAddress: ""
    property string errorText: ""
    property string reachError: ""
    property string socketHint: ""

    signal primaryClicked()
    signal panicClicked()

    readonly property bool active: state === "connected" || state === "reconnecting" || state === "starting"

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
        spacing: 10

        Label {
            objectName: "statusLabel"
            Layout.fillWidth: true
            text: card.statusText
            font.pixelSize: 30
            font.weight: Font.Medium
            elide: Text.ElideRight
            color: card.state === "connected" ? Material.color(Material.Green)
                 : card.state === "error" ? Material.color(Material.Red)
                 : Material.foreground
        }

        Label {
            objectName: "serverLabel"
            Layout.fillWidth: true
            visible: card.reachable && card.hasProfile
            text: (card.profileName ? card.profileName + " · " : "") + card.serverAddress
            elide: Text.ElideMiddle
            opacity: 0.7
        }

        Label {
            objectName: "errorLabel"
            Layout.fillWidth: true
            visible: card.reachable && card.errorText !== "" && card.state !== "connected"
            text: card.errorText
            wrapMode: Text.WrapAtWordBoundaryOrAnywhere
            maximumLineCount: 3
            elide: Text.ElideRight
            color: Material.color(Material.Red)
        }

        Label {
            objectName: "hintLabel"
            Layout.fillWidth: true
            visible: !card.reachable && card.reachError !== ""
            wrapMode: Text.WrapAtWordBoundaryOrAnywhere
            opacity: 0.8
            text: card.reachError + "\n\n" + qsTr("Is the service running?") + " systemctl start yggtunneld"
                  + (card.reachError.toLowerCase().indexOf("permission") >= 0
                     ? "\n" + qsTr("Your user must be in the group yggtunnel.") : "")
        }

        ActionButton {
            objectName: "primaryButton"
            Layout.fillWidth: true
            Layout.topMargin: 6
            visible: card.reachable
            enabled: !card.busy
            primary: true
            text: !card.hasProfile ? qsTr("Import a profile")
                  : card.active ? qsTr("Disconnect") : qsTr("Connect")
            onClicked: card.primaryClicked()
        }

        ActionButton {
            objectName: "panicButton"
            Layout.fillWidth: true
            visible: card.reachable
            enabled: !card.busy
            text: qsTr("Disable everything")
            onClicked: card.panicClicked()
        }
    }
}
