import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

// The connection history of this session and the node's own log.
Popup {
    id: page
    objectName: "logPage"

    parent: Overlay.overlay
    anchors.centerIn: parent
    width: parent.width - 24
    height: parent.height - 24
    modal: true
    padding: 14

    background: Rectangle {
        radius: 24
        color: Material.dialogColor
        border.width: 1
        border.color: Qt.alpha(Material.foreground, 0.12)
    }

    onOpened: ctl.refreshLog()

    Timer {
        interval: 3000
        repeat: true
        running: page.visible
        onTriggered: ctl.refreshLog()
    }

    function copyCurrent() {
        var area = logTabs.currentIndex === 0 ? historyText : nodeLogText
        area.selectAll()
        area.copy()
        area.deselect()
    }

    contentItem: ColumnLayout {
        spacing: 10

        RowLayout {
            Layout.fillWidth: true
            Label {
                Layout.fillWidth: true
                text: qsTr("Log")
                font.pixelSize: 20
                font.weight: Font.Medium
            }
            ActionButton { text: qsTr("Copy"); implicitHeight: 40; onClicked: page.copyCurrent() }
            ActionButton { text: qsTr("Close"); implicitHeight: 40; primary: true; onClicked: page.close() }
        }

        TabBar {
            id: logTabs
            objectName: "logTabs"
            Layout.fillWidth: true
            TabButton { text: qsTr("Connection") }
            TabButton { text: qsTr("Node") }
        }

        StackLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            currentIndex: logTabs.currentIndex

            ScrollView {
                TextArea {
                    id: historyText
                    objectName: "historyText"
                    readOnly: true
                    leftPadding: 12
                    rightPadding: 12
                    topPadding: 10
                    bottomPadding: 10
                    background: Rectangle { radius: 12; color: Qt.alpha(Material.foreground, 0.05) }
                    wrapMode: TextArea.WrapAnywhere
                    font.family: "monospace"
                    font.pixelSize: 13
                    text: ctl.history.join("\n")
                }
            }
            ScrollView {
                id: nodeScroll
                TextArea {
                    id: nodeLogText
                    objectName: "nodeLogText"
                    readOnly: true
                    leftPadding: 12
                    rightPadding: 12
                    topPadding: 10
                    bottomPadding: 10
                    background: Rectangle { radius: 12; color: Qt.alpha(Material.foreground, 0.05) }
                    wrapMode: TextArea.WrapAnywhere
                    font.family: "monospace"
                    font.pixelSize: 13
                    text: ctl.nodeLog
                    onTextChanged: Qt.callLater(function () { nodeLogText.cursorPosition = nodeLogText.length })
                }
            }
        }
    }
}
