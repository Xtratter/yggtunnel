import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

ApplicationWindow {
    id: root
    width: 420
    height: 720
    minimumWidth: 360
    minimumHeight: 560
    visible: true
    title: "YggTunnel"

    Material.theme: Material.System
    Material.accent: Material.Teal

    signal importRequested()

    readonly property string statusText: {
        switch (ctl.state) {
        case "off": return qsTr("Off")
        case "starting": return qsTr("Connecting…")
        case "connected": return qsTr("Connected")
        case "reconnecting": return qsTr("Reconnecting…")
        case "error": return qsTr("Error")
        default: return qsTr("Daemon not reachable")
        }
    }

    readonly property int upPeers: {
        var n = 0
        for (var i = 0; i < ctl.peers.length; ++i)
            if (ctl.peers[i].up) ++n
        return n
    }

    // Island messages on transitions; nothing at startup.
    property string previousState: ""
    Connections {
        target: ctl
        function onChanged() {
            var s = ctl.state
            if (s === root.previousState)
                return
            var before = root.previousState
            root.previousState = s
            if (before === "")
                return
            switch (s) {
            case "starting": island.show(qsTr("Connecting…"), true); break
            case "connected": island.show(root.upPeers === 1 ? qsTr("Connected · 1 peer")
                                          : qsTr("Connected · %1 peers").arg(root.upPeers), false); break
            case "reconnecting": island.show(qsTr("Reconnecting…"), true); break
            case "off":
                if (before === "connected" || before === "starting" || before === "reconnecting")
                    island.show(qsTr("Disconnected"), false)
                break
            case "error": island.show(qsTr("Could not connect"), false); break
            }
        }
    }

    Flickable {
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight + 40
        clip: true

        ColumnLayout {
            id: column
            x: 16
            y: 64 // room for the island
            width: parent.width - 32
            spacing: 16

            StatusCard {
                Layout.fillWidth: true
                state: ctl.state
                statusText: root.statusText
                reachable: ctl.daemonReachable
                busy: ctl.busy
                hasProfile: ctl.hasProfile
                profileName: ctl.profileName
                serverAddress: ctl.serverAddress
                errorText: ctl.lastError
                reachError: ctl.reachError
                onPrimaryClicked: {
                    if (!ctl.hasProfile)
                        root.importRequested()
                    else if (active)
                        ctl.down()
                    else
                        ctl.up()
                }
                onPanicClicked: panicDialog.open()
            }
        }
    }

    Island {
        id: island
        anchors.top: parent.top
        anchors.topMargin: 14
        anchors.horizontalCenter: parent.horizontalCenter
        z: 10
    }

    Dialog {
        id: panicDialog
        title: qsTr("Disable everything?")
        modal: true
        anchors.centerIn: parent
        standardButtons: Dialog.Yes | Dialog.No
        Label {
            width: 300
            wrapMode: Text.WordWrap
            text: qsTr("This disconnects and removes every route, rule and DNS setting that YggTunnel added.")
        }
        onAccepted: ctl.panic()
    }
}
