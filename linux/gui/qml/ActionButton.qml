import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material

// A button with an explicit background: filled when `primary`, outlined otherwise.
Button {
    id: btn
    property bool primary: false

    implicitHeight: 48
    flat: true
    font.pixelSize: 15
    font.weight: Font.Medium

    background: Rectangle {
        radius: height / 2
        color: !btn.primary ? "transparent"
               : btn.enabled ? (btn.down ? Qt.darker(Material.accentColor, 1.2) : Material.accentColor)
               : Qt.alpha(Material.foreground, 0.12)
        border.width: btn.primary ? 0 : 1
        border.color: Qt.alpha(Material.foreground, btn.enabled ? 0.25 : 0.1)
    }
    contentItem: Label {
        text: btn.text
        font: btn.font
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
        color: btn.primary && btn.enabled ? "white" : Qt.alpha(Material.foreground, btn.enabled ? 1 : 0.4)
    }
}
