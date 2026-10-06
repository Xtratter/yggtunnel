package io.github.xtratter.yggtunnel

/**
 * What the status island shows, apart from Android (tested): which connection changes are events, and that the
 * same event is not shown twice in a row.
 */
object StatusIslandModel {
    enum class Phase { OFF, STARTING, ON }
    enum class Kind { CONNECTING, CONNECTED, DISCONNECTED, FAILED }

    /** [detail]: CONNECTED — the number of peers, or "server" with a set-up server; FAILED — the reason. */
    data class Event(val kind: Kind, val detail: String = "")

    /** The service changed its state. Only STARTING and OFF are events here: «connected» waits for [connected]. */
    fun onPhase(prev: Phase?, now: Phase, error: String?): Event? = when {
        now == Phase.STARTING -> Event(Kind.CONNECTING)
        now == Phase.OFF && prev != null && prev != Phase.OFF ->
            if (error != null) Event(Kind.FAILED, error) else Event(Kind.DISCONNECTED)
        else -> null
    }

    /** The node is really connected (see NodeStatus.ok). */
    fun connected(ok: Boolean, peers: Int, viaServer: Boolean): Event? =
        if (ok) Event(Kind.CONNECTED, if (viaServer) "server" else peers.toString()) else null

    /** The same kind twice in a row is shown once; a failure is always shown (its reason may differ). */
    class Dedupe {
        private var last: Kind? = null

        fun accept(e: Event?): Event? {
            if (e == null) return null
            if (e.kind == last && e.kind != Kind.FAILED) return null
            last = e.kind
            return e
        }
    }
}
