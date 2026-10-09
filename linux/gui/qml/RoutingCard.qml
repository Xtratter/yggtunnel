import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts

// Which destinations use the tunnel: everything, everything except a list, or only a list.
// The mode is fixed while a connection is up; the lists can be changed at any time.
Pane {
    id: card
    objectName: "routingCard"

    property string mode: "all"         // as stored by the daemon
    property var subnets: []
    property var domains: []
    property bool locked: false         // a connection is starting or up: the mode cannot change
    property bool busy: false
    property string applied: ""         // the mode of the running connection
    property int resolved: 0
    property string resolveError: ""

    signal applyRequested(string mode, var subnets, var domains)

    property string selectedMode: "all"
    property bool dirty: false          // the user edited something that was not applied yet
    property bool pending: false        // an Apply is waiting for the daemon's answer
    property string applyError: ""      // the daemon's answer to the last Apply, nothing else
    property string syncKey: ""

    // Fields follow the daemon only when ITS data changed, never on every poll, so typing is not lost.
    function sync() {
        var key = card.mode + "|" + card.subnets.join(",") + "|" + card.domains.join(",")
        if (key === syncKey)
            return
        syncKey = key
        if (dirty && key !== "")
            return
        selectedMode = card.mode
        subnetsField.text = card.subnets.join("\n")
        domainsField.text = card.domains.join("\n")
    }
    // The edits are given up only when the daemon accepted them; after a refusal they stay to be fixed.
    Connections {
        target: ctl
        function onSettingsFinished(ok, error) {
            if (!card.pending)
                return
            card.pending = false
            card.applyError = ok ? "" : error
            if (ok) {
                card.dirty = false
                card.syncKey = "" // the next status brings the stored (canonical) form into the fields
            }
        }
    }
    onModeChanged: sync()
    onSubnetsChanged: sync()
    onDomainsChanged: sync()
    Component.onCompleted: sync()

    function lines(text) {
        var out = []
        var parts = text.split("\n")
        for (var i = 0; i < parts.length; ++i) {
            var l = parts[i].replace(/\r/g, "").trim()
            if (l !== "")
                out.push(l)
        }
        return out
    }

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
        spacing: 6

        Label {
            text: qsTr("Routing")
            font.pixelSize: 18
            font.weight: Font.Medium
        }

        RadioButton {
            objectName: "modeAll"
            Layout.fillWidth: true
            enabled: !card.locked && !card.busy
            text: qsTr("All traffic through the tunnel")
            checked: card.selectedMode === "all"
            onClicked: { card.selectedMode = "all"; card.dirty = true }
        }
        RadioButton {
            objectName: "modeExclude"
            Layout.fillWidth: true
            enabled: !card.locked && !card.busy
            text: qsTr("All traffic except the list")
            checked: card.selectedMode === "exclude"
            onClicked: { card.selectedMode = "exclude"; card.dirty = true }
        }
        RadioButton {
            objectName: "modeOnly"
            Layout.fillWidth: true
            enabled: !card.locked && !card.busy
            text: qsTr("Only the list through the tunnel")
            checked: card.selectedMode === "only"
            onClicked: { card.selectedMode = "only"; card.dirty = true }
        }
        Label {
            objectName: "modeLockedHint"
            Layout.fillWidth: true
            visible: card.locked
            wrapMode: Text.WordWrap
            opacity: 0.7
            text: qsTr("Disconnect to change this.")
        }

        Label {
            Layout.fillWidth: true
            Layout.topMargin: 6
            visible: card.selectedMode !== "all"
            text: qsTr("Subnets (one per line)")
            opacity: 0.8
        }
        ScrollView {
            Layout.fillWidth: true
            Layout.preferredHeight: 110
            visible: card.selectedMode !== "all"
            TextArea {
                id: subnetsField
                objectName: "subnetsField"
                visible: card.selectedMode !== "all"
                wrapMode: TextArea.NoWrap
                selectByMouse: true
                font.family: "monospace"
                font.pixelSize: 13
                leftPadding: 12
                topPadding: 10
                placeholderText: ""
                background: Rectangle { radius: 12; color: Qt.alpha(Material.foreground, 0.05) }
                onTextEdited: card.dirty = true
            }
        }

        Label {
            Layout.fillWidth: true
            visible: card.selectedMode !== "all"
            text: qsTr("Domains (one per line; subdomains are not included)")
            wrapMode: Text.WordWrap
            opacity: 0.8
        }
        ScrollView {
            Layout.fillWidth: true
            Layout.preferredHeight: 110
            visible: card.selectedMode !== "all"
            TextArea {
                id: domainsField
                objectName: "domainsField"
                visible: card.selectedMode !== "all"
                wrapMode: TextArea.NoWrap
                selectByMouse: true
                font.family: "monospace"
                font.pixelSize: 13
                leftPadding: 12
                topPadding: 10
                background: Rectangle { radius: 12; color: Qt.alpha(Material.foreground, 0.05) }
                onTextEdited: card.dirty = true
            }
        }

        Label {
            objectName: "resolvedLabel"
            Layout.fillWidth: true
            visible: card.applied !== "" && card.applied !== "all" && card.domains.length > 0
            text: qsTr("%1 addresses resolved").arg(card.resolved)
            opacity: 0.7
        }
        Label {
            objectName: "resolveErrorLabel"
            Layout.fillWidth: true
            visible: card.applied !== "" && card.resolveError !== ""
            text: card.resolveError
            wrapMode: Text.WrapAtWordBoundaryOrAnywhere
            maximumLineCount: 3
            elide: Text.ElideRight
            color: Material.color(Material.Red)
        }

        ActionButton {
            objectName: "applyButton"
            Layout.fillWidth: true
            Layout.topMargin: 4
            enabled: !card.busy
            primary: true
            text: qsTr("Apply")
            onClicked: {
                card.pending = true
                card.applyError = ""
                card.applyRequested(card.selectedMode, card.lines(subnetsField.text), card.lines(domainsField.text))
            }
        }
        Label {
            objectName: "routingError"
            Layout.fillWidth: true
            visible: card.applyError !== ""
            text: card.applyError
            wrapMode: Text.WrapAtWordBoundaryOrAnywhere
            maximumLineCount: 4
            elide: Text.ElideRight
            color: Material.color(Material.Red)
        }
    }
}
