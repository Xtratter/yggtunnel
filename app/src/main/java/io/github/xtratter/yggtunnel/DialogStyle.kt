package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import io.github.xtratter.uikit.M3Dialog

/**
 * Styles a dialog before its first frame: [M3Dialog.style] in onShow came one frame late, so the
 * window first appeared with the default dialog background and insets and then jumped into place
 * (looked like sliding in from the side).
 */
fun AlertDialog.prestyle(): AlertDialog {
    create() // builds the window and its views without showing
    M3Dialog.style(this)
    return this
}
