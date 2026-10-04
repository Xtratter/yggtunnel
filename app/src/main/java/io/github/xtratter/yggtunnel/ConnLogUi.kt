package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.text.SpannableStringBuilder
import android.text.Spanned
import android.text.style.ForegroundColorSpan
import android.text.style.StyleSpan
import android.view.Gravity
import android.widget.LinearLayout
import android.widget.ScrollView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale

/**
 * The connection log screen: only the log — newest first, by day, events coloured — with the last day's
 * checks and outages on top. The log's settings and the diagnostics (speed, upload diagnostics, a problem
 * report) open in dialogs of their own. Re-reads the file every 3 s, only when it changed and while the
 * list is scrolled to the top (so it does not jump under the reader).
 */
class ConnLogUi(private val a: MainActivity, private val onChange: () -> Unit) {
    private val prefs = Prefs(a)
    private var onlyEvents = true
    private var shownVersion = -1L
    private val summary = a.text(13f, M3.TEXT2)
    private val filters = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL }
    private val log = a.text(12f, M3.TEXT, Typeface.MONOSPACE).apply { setTextIsSelectable(true) }
    private val scroll = ScrollView(a).apply { addView(log) }
    private val tick = object : Runnable {
        override fun run() { fill(); a.main.postDelayed(this, 3000) }
    }

    fun show() {
        val buttons = LinearLayout(a).apply {
            gravity = Gravity.CENTER_VERTICAL
            addView(M3Widgets.button(a, a.getString(R.string.connlog_settings), M3Widgets.ButtonKind.TONAL) { settingsDialog() }
                .help(R.string.connlog_settings, R.string.h_connlog_settings), LinearLayout.LayoutParams(0, a.dp(40f), 1f).apply { marginEnd = a.dp(8f) })
            addView(M3Widgets.button(a, a.getString(R.string.connlog_diag), M3Widgets.ButtonKind.TONAL) { DiagUi(a) { refresh() }.show() }
                .help(R.string.connlog_diag, R.string.h_connlog_diag), LinearLayout.LayoutParams(0, a.dp(40f), 1f))
        }
        renderFilters()
        // the header stays, only the log scrolls (no scroll inside a scrolling dialog)
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(buttons); add(summary, 10f); add(filters, 6f)
            add(scroll, 8f, (a.resources.displayMetrics.heightPixels * 0.55f).toInt())
        }
        val d = Theme.dialog(a).setTitle(R.string.connlog_title).setView(box)
            .setPositiveButton(R.string.copy) { _, _ -> a.copy(ConnLog.read(a)) }
            .setNeutralButton(R.string.connlog_clear, null)
            .setNegativeButton(R.string.close, null).create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_NEUTRAL).setOnClickListener { ConnLog.clear(a); refresh() }
            a.main.post(tick)
        }
        d.setOnDismissListener { a.main.removeCallbacks(tick); onChange() }
        d.show()
    }

    private fun renderFilters() {
        filters.removeAllViews()
        for ((label, events) in listOf(a.getString(R.string.connlog_events) to true, a.getString(R.string.connlog_all) to false))
            filters.addView(M3Widgets.chip(a, label, onlyEvents == events) {
                onlyEvents = events; renderFilters(); refresh()
            }, LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
    }

    /** Re-render now, at the top. */
    private fun refresh() { shownVersion = -1; scroll.scrollTo(0, 0); fill() }

    private fun fill() {
        val v = ConnLog.version(a)
        if (v == shownVersion || scroll.scrollY > a.dp(8f)) return // unchanged, or the reader is down the list
        shownVersion = v
        val lines = ConnLog.read(a).lines().filter { it.isNotBlank() }
        val now = Calendar.getInstance()
        val (checks, lost) = ConnLog.lastDay(lines, now)
        summary.text = a.getString(R.string.connlog_day, checks, lost)
        val rows = ConnLog.view(lines, onlyEvents, 400, SimpleDateFormat("dd.MM", Locale.ROOT).format(Date()))
        val sb = SpannableStringBuilder()
        for (r in rows) {
            val start = sb.length
            if (r.kind == ConnLog.Kind.DAY) {
                if (sb.isNotEmpty()) sb.append('\n')
                val s = sb.length
                sb.append(if (r.text.isEmpty()) a.getString(R.string.connlog_today) else r.text).append('\n')
                sb.setSpan(StyleSpan(Typeface.BOLD), s, sb.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                sb.setSpan(ForegroundColorSpan(M3.TEXT2), s, sb.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                continue
            }
            sb.append(r.time).append("  ").append(r.text).append('\n')
            val color = when (r.kind) {
                ConnLog.Kind.LOST -> M3.HOT
                ConnLog.Kind.BACK -> M3.OK
                ConnLog.Kind.CHECK -> M3.TEXT2
                else -> M3.TEXT
            }
            sb.setSpan(ForegroundColorSpan(color), start, sb.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            sb.setSpan(ForegroundColorSpan(M3.TEXT3), start, start + r.time.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
        }
        log.text = Privacy.mask(a, if (sb.isEmpty()) a.getString(R.string.connlog_empty) else sb, prefs.server?.optString("host").orEmpty())
        Privacy.track(log)
    }

    /** The log's own settings: on/off and how often. */
    private fun settingsDialog() {
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        fun render() {
            box.removeAllViews()
            box.addView(a.text(13f, M3.TEXT2).apply { setText(R.string.connlog_explain) })
            box.add(M3Widgets.switchRow(a, a.getString(R.string.connlog_on), prefs.connLog) { on ->
                prefs.connLog = on; YggVpnService.monitorChanged(a); render()
            }, 6f)
            box.add(a.text(12.5f, M3.TEXT2).apply { setText(R.string.connlog_every) }, 4f)
            val row = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL }
            for (s in listOf(5, 10, 30, 60)) row.addView(M3Widgets.chip(a, a.getString(R.string.connlog_seconds, s), s == prefs.connLogInterval) {
                prefs.connLogInterval = s; render()
            }, LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
            box.add(row, 4f)
        }
        render()
        Theme.dialog(a).setTitle(R.string.connlog_settings).setView(box)
            .setNegativeButton(R.string.close, null).create().apply { prestyle(); setOnDismissListener { refresh() }; show() }
    }
}

/** Diagnostics: a speed measurement, the upload diagnostics (link TCP state for 2 min), a problem report. */
class DiagUi(private val a: MainActivity, private val onDone: () -> Unit) {
    private val prefs = Prefs(a)
    private val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
    private val tick = object : Runnable {
        override fun run() { showDiag(); a.main.postDelayed(this, 1000) }
    }

    private val diagButton by lazy {
        M3Widgets.button(a, "", M3Widgets.ButtonKind.TONAL) {
            if (JSONObject(Native.diagStatus()).optBoolean("running")) { Native.diagStop(); return@button }
            if (YggVpnService.state != YggVpnService.State.ON) { android.widget.Toast.makeText(a, R.string.speed_need_vpn, android.widget.Toast.LENGTH_SHORT).show(); return@button }
            Diag.start(a)
            showDiag()
        }.help(R.string.diag_title, R.string.h_diag)
    }

    private fun showDiag() {
        val s = JSONObject(Native.diagStatus())
        diagButton.text = if (s.optBoolean("running")) a.getString(R.string.diag_stop, s.optInt("left")) else a.getString(R.string.diag_start)
    }

    fun show() {
        render()
        val d = Theme.dialog(a).setTitle(R.string.connlog_diag).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setNegativeButton(R.string.close, null).create()
        d.prestyle()
        d.setOnShowListener { a.main.post(tick) }
        d.setOnDismissListener { a.main.removeCallbacks(tick); onDone() }
        d.show()
    }

    private fun render() {
        box.removeAllViews()
        // a speed measurement on request only (traffic): through Yggdrasil to the server, written into the log
        if (SpeedTest.outdated(prefs)) box.add(a.text(12.5f, M3.WARN).apply { setText(R.string.speed_outdated) }, 4f)
        if (SpeedTest.of(prefs) != null) box.add(M3Widgets.button(a, a.getString(R.string.speed_measure), M3Widgets.ButtonKind.OUTLINED) {
            if (YggVpnService.state != YggVpnService.State.ON) { android.widget.Toast.makeText(a, R.string.speed_need_vpn, android.widget.Toast.LENGTH_SHORT).show(); return@button }
            android.widget.Toast.makeText(a, R.string.speed_measuring, android.widget.Toast.LENGTH_SHORT).show()
            val app = a.applicationContext
            Thread {
                val line = SpeedTest.measure(app)
                ConnLog.write(app, "${ConnLog.INFO} $line")
                a.main.post { android.widget.Toast.makeText(app, line, android.widget.Toast.LENGTH_LONG).show() }
            }.start()
        }.help(R.string.speed_measure, R.string.h_speed_measure), 4f, a.dp(44f))
        box.add(a.divider(), 12f, a.dp(1f))
        box.add(a.text(15f, face = M3.bold).apply { setText(R.string.diag_title) }, 10f)
        box.add(a.text(12.5f, M3.TEXT2).apply { setText(R.string.diag_explain) }, 4f)
        box.add(a.text(12.5f, M3.TEXT2).apply { setText(R.string.diag_cc) }.help(R.string.diag_cc, R.string.h_diag_cc), 8f)
        val row = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL }
        for (cc in listOf("westwood", "reno")) row.addView(M3Widgets.chip(a, cc, cc == Diag.cc) {
            val r = Native.diagSetCC(cc)
            if (Native.isError(r)) android.widget.Toast.makeText(a, a.getString(R.string.error, r.removePrefix("error: ")), android.widget.Toast.LENGTH_LONG).show()
            else { Diag.cc = cc; android.widget.Toast.makeText(a, a.getString(R.string.diag_cc_set, cc, r.toInt()), android.widget.Toast.LENGTH_SHORT).show() }
            render()
        }, LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
        box.add(row, 4f)
        box.add(a.text(12.5f, M3.TEXT2).apply { setText(R.string.diag_queue) }.help(R.string.diag_queue, R.string.h_diag_queue), 8f)
        val qrow = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL }
        for ((label, bytes) in listOf(a.getString(R.string.diag_queue_kb, 128) to (128 shl 10), a.getString(R.string.diag_queue_none) to 0))
            qrow.addView(M3Widgets.chip(a, label, Native.linkLowat() == bytes) {
                Native.setLinkLowat(bytes); render()
            }, LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
        box.add(qrow, 4f)
        (diagButton.parent as? android.view.ViewGroup)?.removeView(diagButton)
        box.add(diagButton, 8f, a.dp(44f))
        box.add(a.divider(), 12f, a.dp(1f))
        box.add(M3Widgets.button(a, a.getString(R.string.report_send), M3Widgets.ButtonKind.OUTLINED) {
            val app = a.applicationContext
            android.widget.Toast.makeText(a, R.string.report_sending, android.widget.Toast.LENGTH_SHORT).show()
            Thread {
                val msg = Watchdog.manual(app)
                ConnLog.write(app, "${ConnLog.INFO} $msg")
                a.main.post { android.widget.Toast.makeText(app, msg, android.widget.Toast.LENGTH_LONG).show() }
            }.start()
        }.help(R.string.report_send, R.string.h_report), 10f, a.dp(44f))
        showDiag()
    }
}
