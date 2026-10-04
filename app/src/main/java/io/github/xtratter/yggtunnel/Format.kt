package io.github.xtratter.yggtunnel

import android.content.Context

/** Human-readable sizes and durations for the status and the peer list. */
object Format {
    fun bytes(b: Long) = when {
        b >= 1 shl 20 -> "%.1f MB".format(b / 1048576.0)
        b >= 1 shl 10 -> "%.0f KB".format(b / 1024.0)
        else -> "$b B"
    }

    /** The node's uptime as a clock: «4:05», «1:02:03». */
    fun clock(sec: Double): String {
        val s = sec.toLong()
        return if (s >= 3600) "%d:%02d:%02d".format(s / 3600, s / 60 % 60, s % 60) else "%d:%02d".format(s / 60, s % 60)
    }

    /** How long a peer link has been up: «40 с», «12 мин», «3 ч 5 мин». */
    fun age(ctx: Context, sec: Double): String {
        val s = sec.toLong()
        return when {
            s < 60 -> ctx.getString(R.string.age_s, s)
            s < 3600 -> ctx.getString(R.string.age_m, s / 60)
            else -> ctx.getString(R.string.age_hm, s / 3600, s / 60 % 60)
        }
    }

    /** A peer's "12 ms · ↓ 2 KB · ↑ 1 KB · up 3 min" line. */
    fun peerUp(ctx: Context, p: org.json.JSONObject) = ctx.getString(R.string.peer_up,
        p.optDouble("latencyMs").let { if (it > 0) "%.0f".format(it) else "—" },
        bytes(p.optLong("rx")), bytes(p.optLong("tx")), age(ctx, p.optDouble("uptime")))
}
