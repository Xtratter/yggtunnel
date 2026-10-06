package io.github.xtratter.yggtunnel

import android.content.Context
import android.os.Handler
import android.os.Looper
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Hang reports: a background thread checks every second that the main thread still answers; when it has
 * not for 5 s, the stacks of every Java thread and every goroutine (Native.goStacks) are saved to
 * files/hang-*.txt, noted in the connection log and sent to the server (store.sh ACTION=diag) — one report
 * per hang.
 */
object Watchdog {
    private const val LIMIT_MS = 5000L
    @Volatile private var started = false

    fun start(c: Context) {
        if (started) return
        started = true
        val app = c.applicationContext
        ServerCall.appContext = app
        val main = Handler(Looper.getMainLooper())
        Thread({
            var reported = false
            while (true) {
                val seen = AtomicBoolean(false)
                main.post { seen.set(true) }
                var waited = 0L
                while (!seen.get() && waited < LIMIT_MS) { Thread.sleep(250); waited += 250 }
                if (!seen.get()) {
                    if (!reported) { reported = true; runCatching { report(app) } }
                } else reported = false
                Thread.sleep(1000)
            }
        }, "hang-watchdog").apply { isDaemon = true }.start()
    }

    /** A report on request (the tunnel stalls but the screen works): the same stacks plus the node's state and logs. Blocking. */
    fun manual(app: Context): String = Diag.upload(app, "report", snapshot(app, "report on request") + buildString {
        append("\n\n== node status ==\n").append(runCatching { Native.status() }.getOrElse { it.toString() })
        append("\n\n== node log ==\n").append(runCatching { Native.log() }.getOrElse { it.toString() })
        append("\n\n== connection log (last 300) ==\n").append(ConnLog.read(app).lines().takeLast(300).joinToString("\n"))
    })

    private fun report(app: Context) {
        val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date())
        val text = snapshot(app, "hang: the main thread has not answered for ${LIMIT_MS / 1000} s")
        val dir = app.filesDir
        File(dir, "hang-$stamp.txt").writeText(text)
        dir.listFiles { f -> f.name.startsWith("hang-") }?.sortedBy { it.name }?.dropLast(5)?.forEach { it.delete() }
        ConnLog.write(app, "${ConnLog.INFO} " + app.getString(R.string.hang_noted, stamp))
        val msg = Diag.upload(app, "hang", text)
        ConnLog.write(app, "${ConnLog.INFO} $msg")
    }

    /** Every Java thread's and goroutine's stack. */
    private fun snapshot(app: Context, what: String): String {
        val v = runCatching { app.packageManager.getPackageInfo(app.packageName, 0).versionName }.getOrNull()
        val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date())
        return buildString {
            append("# YggTunnel $v $what, $stamp, Android ${android.os.Build.VERSION.RELEASE}\n")
            append("# lanes ${Prefs(app).lanes}, link ${Prefs(app).serverLink}, VPN ${YggVpnService.state}\n\n")
            append("== main thread ==\n")
            Looper.getMainLooper().thread.stackTrace.forEach { append("    at $it\n") }
            append("\n== all Java threads ==\n")
            for ((t, st) in Thread.getAllStackTraces()) {
                append("\n\"${t.name}\" ${t.state}\n")
                st.forEach { append("    at $it\n") }
            }
            append("\n== goroutines ==\n")
            append(runCatching { Native.goStacks() }.getOrElse { "(unavailable: $it)" })
        }
    }
}
