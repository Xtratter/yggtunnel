import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

// The peers the node is connected to (or trying to), read-only.
Pane {
    id: card
    objectName: "peersCard"

    property var peers: []

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
            text: qsTr("Peers")
            font.pixelSize: 18
            font.weight: Font.Medium
        }

        Label {
            objectName: "peersEmpty"
            visible: card.peers.length === 0
            text: qsTr("No peers yet")
            opacity: 0.7
        }

        Repeater {
            model: card.peers
            delegate: ColumnLayout {
                id: row
                required property var modelData
                Layout.fillWidth: true
                spacing: 2

                RowLayout {
                    Layout.fillWidth: true
                    spacing: 10

                    Rectangle {
                        implicitWidth: 10
                        implicitHeight: 10
                        radius: 5
                        color: row.modelData.up ? Material.color(Material.Green)
                             : row.modelData.error !== "" ? Material.color(Material.Red)
                             : Material.color(Material.Grey)
                    }
                    Label {
                        objectName: "peerUri"
                        Layout.fillWidth: true
                        Layout.minimumWidth: 0
                        text: row.modelData.uri
                        elide: Text.ElideMiddle
                        HoverHandler { id: uriHover }
                        ToolTip.visible: uriHover.hovered
                        ToolTip.text: row.modelData.uri
                    }
                    Label {
                        objectName: "peerLatency"
                        visible: row.modelData.up
                        text: qsTr("%1 ms").arg(Math.round(row.modelData.latencyMs))
                        opacity: 0.7
                    }
                }

                Label {
                    objectName: "peerError"
                    Layout.fillWidth: true
                    Layout.minimumWidth: 0
                    Layout.leftMargin: 20
                    visible: !row.modelData.up && row.modelData.error !== ""
                    text: row.modelData.error
                    wrapMode: Text.WrapAnywhere
                    maximumLineCount: 2
                    elide: Text.ElideRight
                    color: Material.color(Material.Red)
                    opacity: 0.85
                    HoverHandler { id: errHover }
                    ToolTip.visible: errHover.hovered
                    ToolTip.text: row.modelData.error
                }
            }
        }
    }
}
