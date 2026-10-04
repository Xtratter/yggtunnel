package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.content.Intent
import android.text.Editable
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Dialog
import io.github.xtratter.uikit.M3Widgets

/**
 * Per-app routing: a mode switch — "bypass the VPN" (blacklist) or "only through the VPN"
 * (whitelist), each with its own list — and launcher apps with search and checkboxes.
 */
object AppsUi {
    private class App(val pkg: String, val label: String, val info: android.content.pm.ApplicationInfo)

    fun show(a: MainActivity, onSaved: () -> Unit) {
        val prefs = Prefs(a)
        var only = prefs.onlyListedApps
        val excluded = prefs.excludedApps.toMutableSet()
        val included = prefs.includedApps.toMutableSet()
        var chosen = if (only) included else excluded
        val pm = a.packageManager
        val apps = pm.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER), 0)
            .map { it.activityInfo.applicationInfo }.distinctBy { it.packageName }
            .filter { it.packageName != a.packageName }
            .map { App(it.packageName, it.loadLabel(pm).toString(), it) }
            // chosen first, then by name
            .sortedWith(compareBy<App>({ it.pkg !in excluded && it.pkg !in included }, { it.label.lowercase() }))
        var shown = apps
        val adapter = object : BaseAdapter() {
            override fun getCount() = shown.size
            override fun getItem(i: Int) = shown[i]
            override fun getItemId(i: Int) = i.toLong()
            override fun getView(i: Int, convert: View?, parent: ViewGroup): View {
                val row = (convert as? LinearLayout) ?: LinearLayout(a).apply {
                    gravity = Gravity.CENTER_VERTICAL; setPadding(a.dp(20f), a.dp(6f), a.dp(16f), a.dp(6f))
                    addView(ImageView(a), LinearLayout.LayoutParams(a.dp(36f), a.dp(36f)))
                    addView(TextView(a).apply { textSize = 15f; setTextColor(M3.TEXT); setPadding(a.dp(14f), 0, a.dp(8f), 0) },
                        LinearLayout.LayoutParams(0, -2, 1f))
                    addView(CheckBox(a).apply { isClickable = false; isFocusable = false; M3Widgets.tint(this) })
                }
                val app = shown[i]
                (row.getChildAt(0) as ImageView).setImageDrawable(app.info.loadIcon(pm))
                (row.getChildAt(1) as TextView).text = app.label
                (row.getChildAt(2) as CheckBox).isChecked = app.pkg in chosen
                return row
            }
        }
        val list = ListView(a).apply {
            this.adapter = adapter; divider = null
            setOnItemClickListener { _, _, i, _ ->
                val pkg = shown[i].pkg
                if (!chosen.remove(pkg)) chosen.add(pkg)
                adapter.notifyDataSetChanged()
            }
        }
        val search = EditText(a).apply {
            hint = a.getString(R.string.apps_search); setSingleLine(); setTextColor(M3.TEXT); setHintTextColor(M3.TEXT3)
            addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, st: Int, c: Int, af: Int) {}
                override fun onTextChanged(s: CharSequence?, st: Int, b: Int, c: Int) {}
                override fun afterTextChanged(e: Editable?) {
                    val q = e.toString().trim().lowercase()
                    shown = if (q.isEmpty()) apps else apps.filter { q in it.label.lowercase() || q in it.pkg }
                    adapter.notifyDataSetChanged()
                }
            })
        }
        val hint = a.text(13f, M3.TEXT2).apply { setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        val modes = LinearLayout(a).apply { setPadding(a.dp(16f), a.dp(4f), a.dp(16f), a.dp(4f)) }
        fun drawModes() {
            modes.removeAllViews()
            for ((isOnly, label) in listOf(false to R.string.apps_mode_bypass, true to R.string.apps_mode_only)) {
                modes.addView(M3Widgets.chip(a, a.getString(label), only == isOnly) {
                    only = isOnly; chosen = if (only) included else excluded
                    drawModes(); adapter.notifyDataSetChanged()
                }.help(R.string.h_apps_t, R.string.h_apps).apply {
                    // one line: shrink the text instead of wrapping (the ✓ makes it longer)
                    maxLines = 1
                    setPadding(a.dp(8f), 0, a.dp(8f), 0)
                    setAutoSizeTextTypeUniformWithConfiguration(10, 14, 1, android.util.TypedValue.COMPLEX_UNIT_SP)
                }, LinearLayout.LayoutParams(0, a.dp(40f), 1f).apply { marginEnd = if (!isOnly) a.dp(8f) else 0 })
            }
            hint.text = a.getString(if (only) R.string.apps_hint_only else R.string.apps_hint)
        }
        drawModes()
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL
            addView(modes)
            addView(hint)
            addView(search, LinearLayout.LayoutParams(-1, -2).apply { marginStart = a.dp(16f); marginEnd = a.dp(16f) })
            addView(list, LinearLayout.LayoutParams(-1, (a.resources.displayMetrics.heightPixels * 0.5f).toInt()))
        }
        val d = Theme.dialog(a).setTitle(R.string.apps_title).setView(box)
            .setPositiveButton(R.string.save) { _, _ ->
                prefs.onlyListedApps = only; prefs.excludedApps = excluded; prefs.includedApps = included; onSaved()
            }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }
}
