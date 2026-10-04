package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.text.TextUtils
import android.view.Gravity
import android.view.View
import android.widget.CheckBox
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Toast
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import org.json.JSONObject

/**
 * Peer selection: the whole public list (filters: transport, working only); every chosen peer is connected
 * and pinged through a throwaway node (go/peertest.go: connect, ping to our server through the peer, loss),
 * then the speed is measured for the N best by ping; the best — or any ticked ones — become the peer list.
 */
class PeerTestUi(private val a: MainActivity) {
    companion object {
        /** A test is running (the connection log pauses: the tests fill the link). */
        @Volatile var running = false; private set
    }

    private val prefs = Prefs(a)
    private var catalog: List<PeerCatalog.Entry>? = null
    private val chosen = linkedSetOf<String>()
    private var results = emptyList<JSONObject>()
    private lateinit var d: AlertDialog
    private val status = a.text(13f, M3.TEXT2)
    /** Progress: the light part — connected and pinged, the solid part — finished (speed included). */
    private val bar = Bar()
    private val filters = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }
    private val list = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }
    private val target = prefs.server?.optJSONObject("result")?.optString("yggAddress")?.ifEmpty { null }
    private val poll = object : Runnable {
        override fun run() { update(); if (running) a.main.postDelayed(this, 1000) }
    }

    fun show() {
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(status); add(bar.view, 6f, a.dp(8f)); add(filters, 6f); add(list, 8f)
        }
        d = Theme.dialog(a).setTitle(R.string.pt_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.pt_start, null)
            .setNeutralButton(R.string.pt_apply, null)
            .setNegativeButton(R.string.close, null).create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener { if (running) Native.peerTestStop() else start() }
            d.getButton(AlertDialog.BUTTON_NEUTRAL).apply { help(R.string.pt_apply, R.string.h_pt_apply); setOnClickListener { apply() } }
            buttons()
            load()
        }
        d.setOnDismissListener { a.main.removeCallbacks(poll); if (running) { Native.peerTestStop(); running = false } }
        d.show()
    }

    private fun load() {
        status.setText(R.string.peers_catalog_loading)
        Thread {
            val r = runCatching { PeerCatalog.fetchAll(prefs.server) }
            a.main.post {
                catalog = r.getOrNull()
                if (catalog == null) { status.setTextColor(M3.HOT); status.text = a.getString(R.string.peers_catalog_failed, r.exceptionOrNull()?.message ?: "—") }
                renderFilters()
            }
        }.start()
    }

    /** The peers to test: chosen transports, working ones if asked; the most reliable first. */
    private fun candidates(): List<PeerCatalog.Entry> {
        val all = catalog ?: return emptyList()
        return all.filter { it.scheme in prefs.testSchemes && (!prefs.testWorkingOnly || (it.up && it.reliability >= 0.9)) }
            .shuffled().sortedByDescending { it.reliability }
    }

    /** How many will get the speed test: the N best by ping (0 — all of them); 0 when it cannot run. */
    private fun speedN(n: Int) = if (target == null || SpeedTest.of(prefs) == null) 0 else if (prefs.speedCount > 0) minOf(prefs.speedCount, n) else n

    private fun chips(label: Int, help: Int, items: List<Pair<String, Boolean>>, onTap: (Int) -> Unit) {
        filters.add(a.text(12.5f, M3.TEXT2).apply { setText(label) }.help(label, help), 8f)
        val row = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL }
        items.forEachIndexed { i, (name, on) ->
            row.addView(M3Widgets.chip(a, name, on) { if (!running) { onTap(i); renderFilters() } },
                LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
        }
        filters.add(HorizontalScrollView(a).apply { isHorizontalScrollBarEnabled = false; addView(row) }, 4f)
    }

    private fun renderFilters() {
        filters.removeAllViews()
        val all = catalog ?: return
        status.setTextColor(M3.TEXT2)
        status.text = a.getString(R.string.pt_catalog, all.size, all.count { it.up }) +
            if (PeerCatalog.viaServer) " · " + a.getString(R.string.catalog_via_server) else ""
        val schemes = listOf("tls", "quic", "wss", "tcp", "ws")
        chips(R.string.pt_transports, R.string.h_pt_transports, schemes.map { s -> "$s · ${all.count { it.scheme == s }}" to (s in prefs.testSchemes) }) { i ->
            val s = schemes[i]
            prefs.testSchemes = if (s in prefs.testSchemes) prefs.testSchemes - s else prefs.testSchemes + s
        }
        filters.add(M3Widgets.switchRow(a, a.getString(R.string.pt_working), prefs.testWorkingOnly) { on ->
            prefs.testWorkingOnly = on; renderFilters()
        }.help(R.string.pt_working, R.string.h_pt_working), 4f)
        val speed = SpeedTest.of(prefs)
        if (target != null && speed != null) {
            val limits = listOf(10, 20, 50, 100, 0)
            chips(R.string.pt_limit, R.string.h_pt_limit, limits.map { (if (it == 0) a.getString(R.string.pt_all) else "$it") to (it == prefs.speedCount) }) { i ->
                prefs.speedCount = limits[i]
            }
        }
        val bests = listOf(3, 5, 6, 8, 10, 15)
        chips(R.string.pt_best, R.string.h_pt_best, bests.map { "$it" to (it == prefs.bestCount) }) { i ->
            prefs.bestCount = bests[i]
            if (results.isNotEmpty()) { pickBest(); renderList() }
        }
        val n = candidates().size
        val sn = speedN(n)
        filters.add(a.text(12.5f, M3.TEXT3).apply {
            text = a.getString(R.string.pt_will_test, n) + "\n" +
                a.getString(if (target != null) R.string.pt_target else R.string.pt_no_target) +
                (if (speed != null && sn > 0) "\n" + a.getString(R.string.pt_traffic, sn, SpeedTest.BYTES / 1_000_000, sn * SpeedTest.BYTES / 1_000_000, speed.optInt("capMB")) else "")
        }, 8f)
        // the real speed needs the server's speed test: offer to install it (an SSH-set-up server only)
        val s = prefs.server
        if (target != null && speed == null && s != null && !s.optBoolean("imported")) {
            val old = SpeedTest.outdated(prefs)
            filters.add(a.text(12.5f, M3.WARN).apply { setText(if (old) R.string.pt_speed_outdated else R.string.pt_speed_missing) }, 8f)
            filters.add(M3Widgets.button(a, a.getString(if (old) R.string.speed_update else R.string.speed_install), M3Widgets.ButtonKind.TONAL) {
                SpeedTest.install(a) { renderFilters() }
            }.help(R.string.speed_install, R.string.h_speed_install), 6f, a.dp(44f))
        }
    }

    private fun start() {
        val peers = candidates()
        if (peers.isEmpty()) return
        val r = Native.peerTestStart(JSONObject().put("peers", org.json.JSONArray(peers.map { it.uri }))
            .put("target", target ?: "").put("parallel", 24)
            .apply { SpeedTest.of(prefs)?.let { put("speedPort", it.optInt("port")).put("speedToken", it.optString("token")).put("speedBytes", SpeedTest.BYTES).put("speedCount", prefs.speedCount) } }
            .toString())
        if (Native.isError(r)) { status.setTextColor(M3.HOT); status.text = a.getString(R.string.error, r.removePrefix("error: ")); return }
        running = true
        results = emptyList(); chosen.clear(); list.removeAllViews()
        buttons()
        a.main.post(poll)
    }

    private fun update() {
        val s = JSONObject(Native.peerTestStatus())
        val arr = s.getJSONArray("results")
        results = (0 until arr.length()).map { arr.getJSONObject(it) }
        val wasRunning = running
        running = s.optBoolean("running")
        status.setTextColor(M3.TEXT2)
        val total = s.optInt("total").coerceAtLeast(1)
        val pinged = s.optInt("pinged")
        val speedTotal = s.optInt("speedTotal")
        val speedDone = s.optInt("speedDone")
        val speedStage = pinged >= total && speedTotal > 0
        // light — pinged, solid — speeds measured (or pinged, when there is no speed stage)
        bar.set(pinged.toFloat() / total, if (speedN(total) == 0) pinged.toFloat() / total else speedDone.toFloat() / speedTotal.coerceAtLeast(1))
        status.text = if (running) {
            // time left from the pace of the current stage
            val (done, of) = if (speedStage) speedDone to speedTotal else pinged to total
            val left = if (done >= (if (speedStage) 1 else 3)) ((of - done) * s.optDouble("seconds") / done).toLong() else -1
            a.getString(if (speedStage) R.string.pt_progress_speed else R.string.pt_progress_ping, done, of) +
                if (left >= 0) " · " + a.getString(R.string.pt_left, if (left >= 90) a.getString(R.string.pt_min, (left + 30) / 60) else a.getString(R.string.pt_sec, left)) else ""
        } else a.getString(R.string.pt_done, results.count { it.optBoolean("up") }, results.size)
        if (wasRunning && !running) { pickBest(); buttons() }
        renderList()
    }

    /** Ticks the best N that answered (reached the server, when there is one). */
    private fun pickBest() {
        chosen.clear()
        results.filter { it.optBoolean("up") && (target == null || it.optDouble("rttMs", -1.0) >= 0) }
            .take(prefs.bestCount).forEach { chosen += it.getString("uri") }
    }

    private fun renderList() {
        list.removeAllViews()
        for (r in results) {
            val uri = r.getString("uri")
            val up = r.optBoolean("up")
            val row = LinearLayout(a).apply { gravity = Gravity.CENTER_VERTICAL; setPadding(0, a.dp(4f), 0, a.dp(4f)) }
            row.addView(CheckBox(a).apply {
                isChecked = uri in chosen; isEnabled = up && !running; M3Widgets.tint(this)
                setOnCheckedChangeListener { _, on -> if (on) chosen += uri else chosen -= uri; buttons() }
            })
            row.addView(LinearLayout(a).apply {
                orientation = LinearLayout.VERTICAL
                addView(a.text(13f, if (up) M3.TEXT else M3.TEXT3, Typeface.MONOSPACE).apply {
                    text = Privacy.mask(a, uri); Privacy.track(this); maxLines = 1; ellipsize = TextUtils.TruncateAt.MIDDLE
                })
                addView(a.text(12f, if (up) M3.TEXT2 else M3.HOT).apply { text = details(r) })
            }, LinearLayout.LayoutParams(0, -2, 1f))
            list.addView(row)
        }
        buttons()
    }

    private fun details(r: JSONObject): String {
        if (!r.optBoolean("up")) return a.getString(R.string.pt_failed, r.optString("error"))
        val parts = mutableListOf(a.getString(R.string.pt_connect, r.optDouble("connectMs") / 1000))
        val rtt = r.optDouble("rttMs", -1.0)
        if (rtt >= 0) {
            parts += a.getString(R.string.pt_rtt, rtt.toInt())
            if (r.optInt("loss") > 0) parts += a.getString(R.string.pt_loss, r.optInt("loss"))
            if (r.optDouble("kbps") > 0) {
                parts += a.getString(R.string.pt_speed, r.optDouble("kbps") / 1000)
            } else r.optString("speedError").ifEmpty { null }?.let { parts += a.getString(R.string.pt_speed_failed, it) }
        } else if (target != null) parts += r.optString("error").ifEmpty { a.getString(R.string.pt_no_route) }
        else if (r.optDouble("latencyMs") > 0) parts += a.getString(R.string.pt_latency, r.optDouble("latencyMs").toInt())
        return parts.joinToString(" · ")
    }

    /** A thin two-layer progress bar (code-built, like the server panel's). */
    private inner class Bar {
        private val pinged = android.view.View(a).apply { background = M3.pill(a, M3.withAlpha(M3.primary, 0.35f)) }
        private val done = android.view.View(a).apply { background = M3.pill(a, M3.primary) }
        val view = android.widget.FrameLayout(a).apply {
            background = M3.pill(a, M3.ink(0x18)); visibility = android.view.View.GONE
            addView(pinged, android.widget.FrameLayout.LayoutParams(0, -1)); addView(done, android.widget.FrameLayout.LayoutParams(0, -1))
        }
        fun set(p: Float, d: Float) {
            view.visibility = android.view.View.VISIBLE
            view.post {
                val w = view.width
                pinged.layoutParams = pinged.layoutParams.apply { width = (w * p.coerceIn(0f, 1f)).toInt() }
                done.layoutParams = done.layoutParams.apply { width = (w * d.coerceIn(0f, 1f)).toInt() }
                pinged.requestLayout(); done.requestLayout()
            }
        }
    }

    private fun buttons() {
        if (!::d.isInitialized) return
        d.getButton(AlertDialog.BUTTON_POSITIVE)?.setText(if (running) R.string.pt_stop else R.string.pt_start)
        d.getButton(AlertDialog.BUTTON_NEUTRAL)?.apply {
            isEnabled = !running && chosen.isNotEmpty()
            text = if (chosen.isEmpty()) a.getString(R.string.pt_apply) else a.getString(R.string.pt_apply_n, chosen.size)
        }
    }

    /** The ticked peers become the list, in the tested order (best first); our server's peers stay in front. */
    private fun apply() {
        val list = results.map { it.getString("uri") }.filter { it in chosen }
        prefs.setCatalogPeers(list)
        prefs.peersAuto = false // a list picked by hand: the weekly catalog refresh leaves it alone
        a.settingsChanged()
        a.refresh()
        Toast.makeText(a, a.getString(R.string.pt_applied, list.size), Toast.LENGTH_SHORT).show()
        d.dismiss()
    }
}
