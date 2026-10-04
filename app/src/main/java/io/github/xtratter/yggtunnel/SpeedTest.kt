package io.github.xtratter.yggtunnel

import org.json.JSONObject

/**
 * The server's speed test (go/speed.sh): a UDP sender on its Yggdrasil address, with a secret token and a
 * daily cap. Installed on request over SSH; port and token are kept in the server profile under "speed".
 */
object SpeedTest {
    const val BYTES = 2_000_000

    /** Protocol of the app's client (go/speed.go); an older sender on the server must be installed again. */
    const val PROTOCOL = 2

    /** The active server's speed test, or null when not installed. */
    fun installed(prefs: Prefs): JSONObject? = prefs.server?.optJSONObject("speed")?.takeIf { it.optString("token").length == 32 }

    /** The speed test, when installed and speaking this app's protocol. */
    fun of(prefs: Prefs): JSONObject? = installed(prefs)?.takeIf { it.optInt("v", 1) >= PROTOCOL }

    /** Installed, but the old one (0.15–0.17 blasted at full speed and measured only the drops). */
    fun outdated(prefs: Prefs) = installed(prefs) != null && of(prefs) == null

    /** Until when a measurement fills the link: the connection log skips its checks (no false drops). */
    @Volatile var busyUntil = 0L

    /** Installs (or updates) it on the active server with the log window; [done] after success. */
    fun install(a: MainActivity, done: () -> Unit) {
        val prefs = Prefs(a)
        val s = prefs.server ?: return
        SetupRunner.run(a, JSONObject(s.toString()).put("mode", "speed").put("env", JSONObject().put("ACTION", "install")),
            R.string.speed_installing) { r, _ ->
            if (r != null) {
                prefs.server?.takeIf { it.optString("id") == s.optString("id") }?.let { prefs.updateServer(it.put("speed", r)) }
                done()
            }
            r != null
        }
    }

    /** One measurement through the running node (call off the main thread): a log line. */
    fun measure(ctx: android.content.Context): String {
        val prefs = Prefs(ctx)
        val sp = of(prefs) ?: return ctx.getString(if (outdated(prefs)) R.string.speed_outdated else R.string.speed_not_installed)
        val ygg = prefs.server?.optJSONObject("result")?.optString("yggAddress").orEmpty()
        busyUntil = System.currentTimeMillis() + 60_000
        val r = try { Native.speedYgg(ygg, sp.optInt("port"), sp.optString("token"), BYTES) } finally { busyUntil = System.currentTimeMillis() + 3000 }
        if (Native.isError(r)) return ctx.getString(R.string.speed_failed, r.removePrefix("error: "))
        val j = JSONObject(r)
        return ctx.getString(R.string.speed_result, j.optDouble("kbps") / 1000, j.optDouble("avgKbps") / 1000, j.optInt("bytes") / 1_000_000.0)
    }
}
