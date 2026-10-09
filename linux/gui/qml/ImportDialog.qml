import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Dialogs
import QtQuick.Layouts

// Paste a yggtunnel://import#… link or open a file with it; the daemon validates and stores it.
Dialog {
    id: dlg
    objectName: "importDialog"

    property string errorText: ""

    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(parent.width - 32, 400)
    modal: true
    title: qsTr("Import a profile")

    background: Rectangle {
        radius: 24
        color: Material.dialogColor
        border.width: 1
        border.color: Qt.alpha(Material.foreground, 0.12)
    }

    function submit() {
        errorText = ""
        ctl.importLink(linkField.text)
    }

    // aboutToShow, not opened: the daemon can answer before the opening animation ends
    onAboutToShow: errorText = ""
    onOpened: linkField.forceActiveFocus()

    Connections {
        target: ctl
        function onImportFinished(ok, message) {
            if (!dlg.visible)
                return
            if (ok) {
                linkField.text = ""
                dlg.close()
            } else {
                dlg.errorText = message
            }
        }
    }

    FileDialog {
        id: fileDialog
        title: qsTr("Open a profile file")
        onAccepted: { dlg.errorText = ""; ctl.importFile(selectedFile) }
    }

    contentItem: ColumnLayout {
        spacing: 12

        Label {
            Layout.fillWidth: true
            wrapMode: Text.WordWrap
            text: qsTr("Paste the yggtunnel://import#… link, or open a file that contains it.")
            opacity: 0.8
        }

        ScrollView {
            Layout.fillWidth: true
            Layout.preferredHeight: 110
            TextArea {
                id: linkField
                objectName: "linkField"
                wrapMode: TextArea.WrapAnywhere
                selectByMouse: true
                leftPadding: 12
                rightPadding: 12
                topPadding: 10
                background: Rectangle { radius: 12; color: Qt.alpha(Material.foreground, 0.05) }
            }
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: 8
            ActionButton { text: qsTr("Paste"); Layout.fillWidth: true; onClicked: linkField.paste() }
            ActionButton { text: qsTr("Open file…"); Layout.fillWidth: true; onClicked: fileDialog.open() }
        }

        Label {
            objectName: "importError"
            Layout.fillWidth: true
            visible: dlg.errorText !== ""
            text: dlg.errorText
            wrapMode: Text.WrapAtWordBoundaryOrAnywhere
            color: Material.color(Material.Red)
        }

        RowLayout {
            Layout.fillWidth: true
            spacing: 8
            Item { Layout.fillWidth: true }
            ActionButton { text: qsTr("Cancel"); onClicked: dlg.close() }
            ActionButton { text: qsTr("Import"); primary: true; onClicked: dlg.submit() }
        }
    }
}
