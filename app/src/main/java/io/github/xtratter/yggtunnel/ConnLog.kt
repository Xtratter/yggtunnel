package io.github.xtratter.yggtunnel

import android.content.Context
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.TimeUnit

/**
 * The connection log (files/connlog.txt, newest at the end, ~2 MB, the older half dropped beyond that).
 * While the VPN is on, [Monitor] checks every few seconds: 8.8.8.8 through the tunnel, the server through
 * Yggdrasil and the server's public address directly (outside the VPN) — one line per check, and an
 * event line ([EVENT]) when one of them stops or starts answering. VPN on/off and network changes too.
 */
object ConnLog {
    const val EVENT = "‼"
    const val BACK = "✓"
    const val INFO = "●"
    private const val MAX = 2_000_000L
    private val stamp = SimpleDateFormat("dd.MM HH:mm:ss", Locale.ROOT)

    private fun file(ctx: Context) = File(ctx.filesDir, "connlog.txt")

    @Synchronized fun write(ctx: Context, line: String) {
        val f = file(ctx)
        runCatching {
            f.appendText("${stamp.format(Date())}  $line\n")
            if (f.length() > MAX) {
                val t = f.readText()
                f.writeText(t.substring(t.indexOf('\n', t.length / 2) + 1))
            }
        }
    }

    /** Only when the log is on: a VPN or network event. */
    fun event(ctx: Context, line: String) { if (Prefs(ctx).connLog) write(ctx, "$INFO $line") }

    /** A line about an event (not a regular check). */
    fun isEvent(line: String) = EVENT in line || BACK in line || INFO in line

    @Synchronized fun read(ctx: Context): String = runCatching { file(ctx).readText() }.getOrDefault("")

    @Synchronized fun clear(ctx: Context) { file(ctx).delete() }

    /** When the log file last changed (size and time): the screen re-reads it only then. */
    @Synchronized fun version(ctx: Context): Long = file(ctx).let { it.length() * 31 + it.lastModified() }

    // ---- how the log is shown (no Android here: tested) ----

    enum class Kind { DAY, CHECK, INFO, LOST, BACK }
    data class Row(val kind: Kind, val time: String, val text: String)

    /** A line's time ("dd.MM HH:mm:ss") as a moment near [now] (the year is not stored: the nearest past one). */
    fun timeOf(line: String, now: java.util.Calendar): Long? {
        val m = Regex("""^(\d\d)\.(\d\d) (\d\d):(\d\d):(\d\d)""").find(line) ?: return null
        val (d, mo, h, mi, se) = m.destructured
        val c = (now.clone() as java.util.Calendar).apply {
            set(get(java.util.Calendar.YEAR), mo.toInt() - 1, d.toInt(), h.toInt(), mi.toInt(), se.toInt()); set(java.util.Calendar.MILLISECOND, 0)
        }
        if (c.timeInMillis > now.timeInMillis + 86_400_000L) c.add(java.util.Calendar.YEAR, -1)
        return c.timeInMillis
    }

    /** Checks and outages in the last 24 h. */
    fun lastDay(lines: List<String>, now: java.util.Calendar): Pair<Int, Int> {
        val from = now.timeInMillis - 86_400_000L
        var checks = 0; var lost = 0
        for (l in lines.asReversed()) {
            val t = timeOf(l, now) ?: continue
            if (t < from) break
            when {
                EVENT in l -> lost++
                !isEvent(l) -> checks++
            }
        }
        return checks to lost
    }

    /** Newest first, a header row per day ([today] for today's), at most [max] lines; only events if [events]. */
    fun view(lines: List<String>, events: Boolean, max: Int, today: String): List<Row> {
        val todayKey = today
        val out = mutableListOf<Row>()
        var day: String? = null
        var n = 0
        for (l in lines.asReversed()) {
            if (l.length < 16 || (events && !isEvent(l))) continue
            val d = l.substring(0, 5)
            if (d != day) { day = d; out += Row(Kind.DAY, "", d) }
            val text = l.substring(14).trim()
            val kind = when {
                text.startsWith(EVENT) -> Kind.LOST
                text.startsWith(BACK) -> Kind.BACK
                text.startsWith(INFO) -> Kind.INFO
                else -> Kind.CHECK
            }
            out += Row(kind, l.substring(6, 14), text)
            if (++n >= max) break
        }
        return out.map { if (it.kind == Kind.DAY && it.text == todayKey) it.copy(text = "") else it }
    }

    /** Pings with the system ping — this app is outside the VPN, so it goes straight out. ms or null. */
    fun systemPing(host: String, timeoutS: Int): Double? = runCatching {
        val p = ProcessBuilder("/system/bin/ping", "-c", "1", "-W", "$timeoutS", host).redirectErrorStream(true).start()
        if (!p.waitFor(timeoutS + 3L, TimeUnit.SECONDS)) { p.destroy(); return null }
        Regex("time=([0-9.]+)").find(p.inputStream.bufferedReader().readText())?.groupValues?.get(1)?.toDouble()
    }.getOrNull()

    /**
     * What the checks mean for the log, kept apart from Android so it can be tested: a target counts as down
     * only after [MISSES] misses in a row (one lost probe under a full line — a speed test — is not an outage;
     * it still shows as «—» in the check line), the outage then dates from the first miss; and a check that
     * comes much later than due means the checks were not running (the phone slept or the app was paused).
     */
    class Judge(private var intervalMs: Long) {
        companion object { const val MISSES = 2 }
        private val misses = mutableMapOf<String, Int>()
        private val firstMiss = mutableMapOf<String, Long>()
        private val down = mutableSetOf<String>()
        private var last = 0L

        sealed interface Note
        data class Lost(val name: String) : Note
        data class Back(val name: String, val gapS: Long) : Note
        data class Paused(val seconds: Long) : Note

        /** A check skipped on purpose (our own speed test): not a pause. */
        fun skip(now: Long) { last = now }

        /** [interval] — the current setting: it can change while the VPN is on (0.33 kept the one from the
         *  start and, switched from 10 to 30 s, logged «no checks for 30 s» at every check). */
        fun check(now: Long, results: List<Pair<String, Double?>>, interval: Long = intervalMs): List<Note> {
            intervalMs = interval
            val out = mutableListOf<Note>()
            if (last > 0 && now - last > intervalMs * 2 + 10_000) out += Paused((now - last) / 1000)
            last = now
            for ((name, ms) in results) {
                if (ms == null) {
                    val n = (misses[name] ?: 0) + 1
                    misses[name] = n
                    if (n == 1) firstMiss[name] = now
                    if (n == MISSES && down.add(name)) out += Lost(name)
                } else {
                    if (down.remove(name)) out += Back(name, (now - (firstMiss[name] ?: now)) / 1000)
                    misses.remove(name); firstMiss.remove(name)
                }
            }
            return out
        }
    }

    /** The checks, on a thread of their own, while the VPN is on. */
    class Monitor(private val ctx: Context) {
        @Volatile private var on = true

        fun start() = Thread {
            val prefs = Prefs(ctx)
            val judge = Judge(prefs.connLogInterval * 1000L)
            // WireGuard's handshake and Yggdrasil's paths take a few seconds after (re)connecting: checking at 3 s
            // logged a false «does not answer» after every reconnect
            Thread.sleep(8000)
            while (on) {
                runCatching { check(prefs, judge) }
                Thread.sleep(prefs.connLogInterval * 1000L)
            }
        }.apply { isDaemon = true; start() }

        fun stop() { on = false }

        private fun check(prefs: Prefs, judge: Judge) {
            // the link is full on purpose; skipped checks are not a pause
            if (System.currentTimeMillis() < SpeedTest.busyUntil || PeerTestUi.running) { judge.skip(System.currentTimeMillis()); return }
            val srv = prefs.server
            val ygg = srv?.optJSONObject("result")?.optString("yggAddress")?.ifEmpty { null }
            val host = srv?.optString("host")?.ifEmpty { null }
            val targets = listOfNotNull(
                if (prefs.tunnelServer != null) "8.8.8.8" to { Native.pingInet("8.8.8.8", 3000).toDoubleOrNull() } else null,
                ygg?.let { ctx.getString(R.string.log_server) to { Native.pingYgg(it, 3000).toDoubleOrNull() } },
                host?.let { ctx.getString(R.string.log_public) to { systemPing(it, 3) } }, // the host is in the server card: not repeated in every line
            )
            if (targets.isEmpty() || !on) return
            // all at once, each on a thread of its own with a common deadline: a stuck probe is left behind instead
            // of holding up the next checks (a pool of 3 filled up and checks came every 30–50 s instead of 10)
            val waits = targets.map { (name, f) ->
                val r = java.util.concurrent.atomic.AtomicReference<Double?>()
                val t = Thread { r.set(runCatching(f).getOrNull()) }.apply { isDaemon = true; start() }
                Triple(name, t, r)
            }
            val until = System.currentTimeMillis() + 5000
            val res = waits.map { (name, t, r) -> t.join((until - System.currentTimeMillis()).coerceAtLeast(1)); name to r.get() }
            if (!on) return
            write(ctx, res.joinToString(" · ") { (name, ms) -> "$name ${ms?.let { "%.0f".format(Locale.ROOT, it) } ?: "—"}" })
            for (note in judge.check(System.currentTimeMillis(), res, prefs.connLogInterval * 1000L)) when (note) {
                is Judge.Paused -> write(ctx, "$INFO ${ctx.getString(R.string.log_paused, note.seconds)}")
                is Judge.Lost -> write(ctx, "$EVENT ${ctx.getString(R.string.log_lost, note.name)}")
                is Judge.Back -> write(ctx, "$BACK ${ctx.getString(R.string.log_back, note.name, note.gapS)}")
            }
        }
    }
}
