package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.content.ClipboardManager
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Typeface
import android.text.InputType
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Toast
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Dialog
import org.json.JSONObject

/** Showing a device profile as a QR code / link, and importing one. */
object ProfileUi {

    private fun qrBitmap(text: String): Bitmap? {
        val m = runCatching { JSONObject(Native.qr(text)) }.getOrNull() ?: return null
        val size = m.getInt("size"); val rows = m.getJSONArray("rows")
        val quiet = 4; val full = size + quiet * 2
        val bmp = Bitmap.createBitmap(full, full, Bitmap.Config.ARGB_8888)
        bmp.eraseColor(Color.WHITE)
        for (y in 0 until size) {
            val row = rows.getString(y)
            for (x in 0 until size) if (row[x] == '1') bmp.setPixel(x + quiet, y + quiet, Color.BLACK)
        }
        return bmp
    }

    fun show(a: MainActivity, link: String, name: String, note: String? = null) {
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.device_explain) })
            qrBitmap(link)?.let { bmp ->
                addView(ImageView(a).apply {
                    // nearest-neighbour scaling keeps the modules sharp
                    setImageBitmap(Bitmap.createScaledBitmap(bmp, bmp.width * 8, bmp.height * 8, false))
                    adjustViewBounds = true; setBackgroundColor(Color.WHITE)
                    setPadding(a.dp(8f), a.dp(8f), a.dp(8f), a.dp(8f))
                }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = a.dp(12f) })
            }
            addView(a.text(12f, M3.WARN).apply {
                text = listOfNotNull(note, a.getString(R.string.device_warning)).joinToString("\n\n"); setPadding(0, a.dp(12f), 0, 0)
            })
        }
        val d = Theme.dialog(a).setTitle(a.getString(R.string.device_title, name)).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.share) { _, _ ->
                a.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, link), null))
            }
            .setNeutralButton(R.string.copy) { _, _ -> a.copy(link) }
            .setNegativeButton(R.string.close, null)
            .create()
        d.prestyle()
        d.setOnShowListener { d.getButton(AlertDialog.BUTTON_POSITIVE).help(R.string.h_share_t, R.string.h_share) }
        d.show()
    }

    /** "Import profile": paste field, prefilled from the clipboard when it holds a profile. */
    fun paste(a: MainActivity) {
        val clip = runCatching { a.getSystemService(ClipboardManager::class.java).primaryClip?.getItemAt(0)?.coerceToText(a)?.toString() }.getOrNull()
        val edit = EditText(a).apply {
            hint = "yggtunnel://import#…"; setText(clip?.takeIf { Profile.parse(it) != null } ?: "")
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            typeface = Typeface.MONOSPACE; textSize = 11f; maxLines = 4; setTextColor(M3.TEXT); setHintTextColor(M3.TEXT3)
        }
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.profile_import_hint) })
            addView(edit)
        }
        val d = Theme.dialog(a).setTitle(R.string.profile_import).setView(box)
            .setPositiveButton(R.string.profile_import_do) { _, _ -> confirm(a, edit.text.toString()) }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    /** Asks before replacing the current server with the profile in [text]. */
    fun confirm(a: MainActivity, text: String) {
        val p = Profile.parse(text)
        if (p == null) { Toast.makeText(a, R.string.profile_bad, Toast.LENGTH_LONG).show(); return }
        val prefs = Prefs(a)
        val d = Theme.dialog(a).setTitle(R.string.profile_import)
            .setMessage(a.getString(if (prefs.server != null) R.string.profile_replace else R.string.profile_confirm, p.optString("name")))
            .setPositiveButton(R.string.profile_import_do) { _, _ ->
                Profile.import(prefs, p)
                a.refreshAll()
                a.settingsChanged()
                Toast.makeText(a, R.string.profile_done, Toast.LENGTH_LONG).show()
            }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }
}
