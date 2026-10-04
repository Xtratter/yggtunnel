package io.github.xtratter.yggtunnel

import android.content.Context
import android.content.SharedPreferences

/**
 * Every settings change goes into the connection log («Setting: Parallel links to the server → 3»), so a
 * measurement can be matched with what was set at the time. Secrets are never written (keys, server data:
 * only «changed»); bookkeeping values (peer-test filters, timestamps) are skipped.
 */
object SettingsLog {
    // SharedPreferences keeps listeners weakly: hold it here
    private var listener: SharedPreferences.OnSharedPreferenceChangeListener? = null
    private var last = "" // one change can touch several keys (app lists): the same line once
    private var lastAt = 0L

    fun install(ctx: Context) {
        if (listener != null) return
        val app = ctx.applicationContext
        listener = SharedPreferences.OnSharedPreferenceChangeListener { p, key ->
            val line = key?.let { runCatching { describe(app, p, it) }.getOrNull() } ?: return@OnSharedPreferenceChangeListener
            val now = System.currentTimeMillis()
            if (line == last && now - lastAt < 2000) return@OnSharedPreferenceChangeListener
            last = line; lastAt = now
            ConnLog.event(app, app.getString(R.string.log_setting, line))
        }
        app.getSharedPreferences("prefs", Context.MODE_PRIVATE).registerOnSharedPreferenceChangeListener(listener)
    }

    private fun onOff(c: Context, v: Boolean) = c.getString(if (v) R.string.log_on_short else R.string.log_off_short)

    /** «label → value», or null for what is not logged. */
    fun describe(c: Context, p: SharedPreferences, key: String): String? {
        fun b(label: Int) = "${c.getString(label)} → ${onOff(c, p.getBoolean(key, false))}"
        fun n(label: Int) = "${c.getString(label)} → ${p.all[key]}"
        return when (key) {
            "server_lanes" -> n(R.string.lanes_title)
            "server_link" -> "${c.getString(R.string.server_link)} → ${p.getString(key, "")?.uppercase()}"
            "keep_peers" -> p.getInt(key, 0).let { "${c.getString(R.string.auto_peers_title)} → ${if (it == 0) c.getString(R.string.keep_off) else it}" }
            "server_only_peers" -> b(R.string.server_only)
            "own_peers" -> b(R.string.own_peers)
            "full_tunnel" -> b(R.string.full_tunnel)
            "auto_reconnect" -> b(R.string.auto_reconnect)
            "auto_connect" -> b(R.string.auto_connect)
            "status_notification" -> b(R.string.status_notification)
            "hide_addresses" -> b(R.string.hide_addresses)
            "translucent" -> b(R.string.translucency)
            "theme" -> "${c.getString(R.string.theme)} → ${c.getString(Prefs(c).theme.title)}"
            "conn_log" -> b(R.string.connlog_title)
            "conn_log_interval" -> "${c.getString(R.string.connlog_title)} → ${c.getString(R.string.connlog_seconds, p.getInt(key, 10))}"
            "only_listed_apps", "included_apps", "excluded_apps" -> {
                val only = p.getBoolean("only_listed_apps", false)
                val count = (p.getStringSet(if (only) "included_apps" else "excluded_apps", emptySet()) ?: emptySet()).size
                "${c.getString(R.string.apps_title)} → ${c.getString(if (only) R.string.log_apps_only else R.string.log_apps_bypass, count)}"
            }
            "peers", "peers_auto" -> {
                val n = Prefs(c).effectivePeers.size
                "${c.getString(R.string.peers)} → ${c.getString(if (Prefs(c).peersAuto) R.string.log_peers_catalog else R.string.log_peers_hand, n)}"
            }
            "server", "servers", "active_server" -> "${c.getString(R.string.server_title_log)} → ${c.getString(R.string.log_changed)}"
            else -> null // secrets, timestamps, peer-test filters, session bookkeeping
        }
    }
}
