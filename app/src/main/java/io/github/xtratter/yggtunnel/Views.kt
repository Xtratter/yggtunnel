package io.github.xtratter.yggtunnel

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Typeface
import android.view.Gravity
import android.view.View
import android.text.InputType
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.TextView
import android.widget.Toast
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Surface
import io.github.xtratter.uikit.M3Widgets

/** Small view helpers shared by the screen and its dialogs (code-built UI, no XML layouts). */

fun Context.dp(v: Float) = M3.dp(this, v).toInt()

fun Context.text(size: Float, color: Int = M3.TEXT, face: Typeface = M3.regular) = TextView(this).apply {
    textSize = size; setTextColor(color); typeface = face
}

/** A rounded M3 surface holding a vertical column — one card of the main screen. */
fun Context.card() = LinearLayout(this).apply {
    orientation = LinearLayout.VERTICAL
    background = M3Surface(this@card, 28f)
    setPadding(dp(20f), dp(18f), dp(20f), dp(18f))
}

/** Adds [v] full-width with [top] dp above it; [h] — a fixed height in px or -2 (wrap). */
fun LinearLayout.add(v: View, top: Float = 0f, h: Int = -2) =
    addView(v, LinearLayout.LayoutParams(-1, h).apply { topMargin = context.dp(top) })

/** A tappable settings row: title and a summary line below; returns the row and the summary to update later. */
fun Context.settingsRow(title: Int, onClick: () -> Unit): Pair<LinearLayout, TextView> {
    val summary = text(13f, M3.TEXT2)
    val row = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL; minimumHeight = dp(52f); gravity = Gravity.CENTER_VERTICAL
        background = M3.ripple(this@settingsRow, 16f)
        addView(text(16f).apply { setText(title) })
        addView(summary)
        setOnClickListener { onClick() }
    }
    return row to summary
}

/** Caps a scrolling dialog body at 60% of the screen, so a long log never pushes the buttons out. */
fun Context.bounded(v: View): View = object : FrameLayout(this) {
    override fun onMeasure(widthSpec: Int, heightSpec: Int) {
        val max = (resources.displayMetrics.heightPixels * 0.6f).toInt()
        val size = MeasureSpec.getSize(heightSpec)
        val capped = if (MeasureSpec.getMode(heightSpec) == MeasureSpec.UNSPECIFIED || size > max) max else size
        super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(capped, MeasureSpec.AT_MOST))
    }
}.apply { addView(v) }

fun Context.copy(s: String) {
    getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("YggTunnel", s))
    Toast.makeText(this, R.string.copied, Toast.LENGTH_SHORT).show()
}

/** A themed single text field. */
fun Context.field(hint: Int, value: String, type: Int = InputType.TYPE_CLASS_TEXT) = EditText(this).apply {
    this.hint = getString(hint); setText(value); inputType = type
    setTextColor(M3.TEXT); setHintTextColor(M3.TEXT3); textSize = 15f
}

/** A themed radio button for the choice dialogs. */
fun Context.radio(label: String, checked: Boolean, onClick: () -> Unit) = RadioButton(this).apply {
    id = View.generateViewId(); text = label; textSize = 16f; setTextColor(M3.TEXT); minHeight = dp(46f)
    isChecked = checked; M3Widgets.tint(this)
    setOnClickListener { onClick() }
}

/** A thin divider line inside a card. */
fun Context.divider() = View(this).apply { setBackgroundColor(M3.ink(0x22)) }

/** No copy/cut/share in the field's menus: a saved or pasted secret (SSH key, token) can be replaced, never taken out. */
fun EditText.noCopy() {
    customSelectionActionModeCallback = object : android.view.ActionMode.Callback {
        override fun onCreateActionMode(m: android.view.ActionMode, menu: android.view.Menu) = true
        override fun onPrepareActionMode(m: android.view.ActionMode, menu: android.view.Menu): Boolean {
            for (id in listOf(android.R.id.copy, android.R.id.cut, android.R.id.shareText)) menu.removeItem(id)
            return true
        }
        override fun onActionItemClicked(m: android.view.ActionMode, item: android.view.MenuItem) = false
        override fun onDestroyActionMode(m: android.view.ActionMode) {}
    }
}
