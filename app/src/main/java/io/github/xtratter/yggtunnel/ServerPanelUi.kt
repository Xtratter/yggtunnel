package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.view.View
import android.widget.LinearLayout
import android.widget.ScrollView
import io.github.xtratter.uikit.M3
import org.json.JSONObject

/**
 * The server panel: a read-only snapshot over SSH (go/status.sh) — system, network, services,
 * Yggdrasil, WireGuard, updates — refreshed every 15 s while open; Update packages on confirmation.
 */
class ServerPanelUi(private val a: MainActivity, private val server: JSONObject) {
    private val status = a.text(13f, M3.TEXT2)
    private val body = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }
    private var dialog: AlertDialog? = null
    private var busy = false
    private val auto = object : Runnable {
        override fun run() { load(); a.main.postDelayed(this, 15_000) }
    }

    fun show() {
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(status); add(body, 4f)
        }
        val d = Theme.dialog(a).setTitle(R.string.panel_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setNeutralButton(R.string.refresh, null)
            .setPositiveButton(R.string.panel_upgrade, null)
            .setNegativeButton(R.string.close, null).create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_NEUTRAL).apply { help(R.string.refresh, R.string.h_panel_refresh); setOnClickListener { load() } }
            d.getButton(AlertDialog.BUTTON_POSITIVE).apply { help(R.string.panel_upgrade, R.string.h_panel_upgrade); visibility = View.GONE; setOnClickListener { confirmUpgrade() } }
            a.main.post(auto)
        }
        d.setOnDismissListener { a.main.removeCallbacks(auto) }
        dialog = d
        d.show()
    }

    private fun load() {
        if (busy) return
        busy = true
        status.setTextColor(M3.TEXT2); status.setText(R.string.devices_loading)
        ServerCall.run(server, "status", JSONObject().put("ACTION", "status")) { r, err ->
            busy = false
            if (r == null) { status.setTextColor(M3.HOT); status.text = a.getString(R.string.error, err.orEmpty()); return@run }
            status.text = a.getString(R.string.panel_updated, java.text.DateFormat.getTimeInstance().format(java.util.Date()))
            render(r)
        }
    }

    private fun section(title: Int) = body.add(a.text(15f, face = M3.bold).apply { setText(title) }, 12f)
    private fun line(s: CharSequence, color: Int = M3.TEXT) = body.add(a.text(13.5f, color).apply { text = s }, 3f)

    /** A thin usage bar: [part] of 1, warn colour above 80 %. */
    private fun bar(part: Double) = body.add(LinearLayout(a).apply {
        background = M3.pill(a, M3.ink(0x18))
        val f = part.coerceIn(0.0, 1.0).toFloat()
        addView(View(a).apply { background = M3.pill(a, if (f > 0.8f) M3.WARN else M3.primary) }, LinearLayout.LayoutParams(0, -1, f))
        addView(View(a), LinearLayout.LayoutParams(0, -1, 1f - f))
    }, 3f, a.dp(6f))

    private fun render(r: JSONObject) {
        body.removeAllViews()
        val gb = { b: Long -> "%.1f GB".format(b / 1e9) }
        section(R.string.panel_system)
        line(Privacy.mask(a, "${r.optString("hostname")} · ${r.optString("os")}", r.optString("hostname")))
        line(a.getString(R.string.panel_uptime, Format.clock(r.optDouble("uptime")).let { up ->
            val d = (r.optDouble("uptime") / 86400).toLong(); if (d > 0) a.getString(R.string.panel_days, d) else up }), M3.TEXT2)
        r.optJSONArray("load")?.takeIf { it.length() == 3 }?.let { l ->
            line(a.getString(R.string.panel_load, l.getDouble(0), l.getDouble(1), l.getDouble(2), r.optInt("cpus")), M3.TEXT2)
        }
        if (!r.isNull("cpu")) { line(a.getString(R.string.panel_cpu, r.optDouble("cpu"))); bar(r.optDouble("cpu") / 100) }
        val memT = r.optLong("memTotal"); val memU = memT - r.optLong("memAvailable")
        if (memT > 0) { line(a.getString(R.string.panel_mem, Format.bytes(memU), Format.bytes(memT))); bar(memU.toDouble() / memT) }
        val dT = r.optLong("diskTotal"); val dU = r.optLong("diskUsed")
        if (dT > 0) { line(a.getString(R.string.panel_disk, gb(dU), gb(dT))); bar(dU.toDouble() / dT) }

        section(R.string.panel_network)
        line(a.getString(R.string.panel_rate, Format.bytes(r.optLong("rxRate")), Format.bytes(r.optLong("txRate"))))
        line(a.getString(R.string.panel_total, r.optString("iface"), Format.bytes(r.optLong("rx")), Format.bytes(r.optLong("tx"))), M3.TEXT2)

        r.optJSONObject("services")?.let { sv ->
            section(R.string.panel_services)
            for (k in sv.keys()) {
                val st = sv.getString(k)
                line("${if (st == "active") "●" else "○"} $k — $st", if (st == "active") M3.OK else M3.HOT)
            }
        }
        r.optJSONObject("ygg")?.takeIf { it.length() > 0 }?.let { y ->
            section(R.string.panel_ygg)
            line(a.getString(R.string.panel_ygg_peers, y.optInt("up"), y.optInt("peers")), if (y.optInt("up") > 0) M3.TEXT else M3.HOT)
        }
        r.optJSONObject("wg")?.takeIf { it.length() > 0 }?.let { w ->
            section(R.string.panel_wg)
            line(a.getString(R.string.panel_wg_online, w.optInt("online"), w.optInt("devices")))
        }
        section(R.string.panel_updates)
        val n = r.optInt("upgradable")
        line(if (n == 0) a.getString(R.string.panel_up_to_date) else a.resources.getQuantityString(R.plurals.panel_upgradable, n, n), if (n == 0) M3.TEXT2 else M3.WARN)
        if (r.optBoolean("rebootRequired")) line(a.getString(R.string.panel_reboot), M3.WARN)
        dialog?.getButton(AlertDialog.BUTTON_POSITIVE)?.visibility = if (n > 0) View.VISIBLE else View.GONE
    }

    /** apt update + upgrade, with the log — only after the user confirms (it can restart services). */
    private fun confirmUpgrade() {
        Theme.dialog(a).setTitle(R.string.panel_upgrade).setMessage(R.string.panel_upgrade_text)
            .setPositiveButton(R.string.panel_upgrade) { _, _ ->
                a.main.removeCallbacks(auto)
                SetupRunner.run(a, JSONObject(server.toString()).put("mode", "status").put("env", JSONObject().put("ACTION", "upgrade")),
                    R.string.panel_upgrading) { r, _ -> r?.let { render(it) }; a.main.postDelayed(auto, 15_000); r != null }
            }
            .setNegativeButton(android.R.string.cancel, null).create().apply { prestyle(); show() }
    }
}
