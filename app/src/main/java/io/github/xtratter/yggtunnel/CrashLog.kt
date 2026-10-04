package io.github.xtratter.yggtunnel

import android.app.Activity
import android.content.Context
import java.io.File

/**
 * Saves the stack trace of a crash to the app's files and shows it on the next start (with Copy),
 * so a crash can be reported without adb.
 */
object CrashLog {
    private fun file(ctx: Context) = File(ctx.filesDir, "crash.txt")
    private fun goFile(ctx: Context) = File(ctx.filesDir, "go-crash.txt")
    private fun goPrev(ctx: Context) = File(ctx.filesDir, "go-crash-prev.txt")
    @Volatile private var redirected = false

    fun install(ctx: Context) {
        val app = ctx.applicationContext
        SettingsLog.install(app) // called by the app and the service alike: settings changes into the connection log
        // Go's panic / fatal error trace (fd 2): what the last run left is kept for take(), then a fresh file
        if (!redirected) {
            redirected = true
            runCatching {
                val g = goFile(app)
                if (g.length() > 0) g.renameTo(goPrev(app)) else g.delete()
                Native.redirectStderr(g.path)
            }
        }
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { t, e ->
            runCatching {
                val v = runCatching { app.packageManager.getPackageInfo(app.packageName, 0).versionName }.getOrNull()
                file(app).writeText("YggTunnel $v, Android ${android.os.Build.VERSION.RELEASE}, thread ${t.name}\n\n${e.stackTraceToString()}")
            }
            previous?.uncaughtException(t, e)
        }
    }

    /** The saved crash (and forgets it), or null; a Go crash is also sent to the server. */
    fun take(a: Activity): String? {
        val java = file(a).takeIf { it.exists() }?.let { f -> runCatching { f.readText() }.getOrNull().also { f.delete() } }
        val go = goPrev(a).takeIf { it.exists() }?.let { f -> runCatching { f.readText() }.getOrNull().also { f.delete() } }
            ?.takeIf { it.isNotBlank() }
        if (go != null) {
            val app = a.applicationContext
            val v = runCatching { app.packageManager.getPackageInfo(app.packageName, 0).versionName }.getOrNull()
            Thread { ConnLog.write(app, "${ConnLog.INFO} " + Diag.upload(app, "crash", "# YggTunnel $v Go crash (previous run)\n\n$go")) }.start()
        }
        return listOfNotNull(java, go?.let { "Go:\n$it" }).joinToString("\n\n").ifEmpty { null }
    }
}
