package io.github.xtratter.yggtunnel

import android.os.Handler
import android.os.Looper
import org.json.JSONObject

/**
 * Runs one of the embedded server scripts over SSH without a log window (devices, the server panel):
 * [done] gets the script's YGGTUNNEL_RESULT or an error message, on the main thread. The Go side runs
 * one script at a time; a second call while one is running fails with an error.
 */
object ServerCall {
    private val main = Handler(Looper.getMainLooper())

    /** [run] for a background thread: waits for the script; the result, or null and the error. */
    fun runBlocking(server: JSONObject, mode: String, env: JSONObject, timeoutMs: Long = 90_000): Pair<JSONObject?, String?> {
        val started = Native.setupStart(JSONObject(server.toString()).put("mode", mode).put("env", env).toString())
        if (Native.isError(started)) return null to started.removePrefix("error: ")
        val end = System.currentTimeMillis() + timeoutMs
        while (System.currentTimeMillis() < end) {
            val st = JSONObject(Native.setupStatus())
            if (!st.optBoolean("running")) {
                st.optJSONObject("result")?.let { return it to null }
                val err = st.optString("log").lines().lastOrNull { it.startsWith("error: ") && "script failed" !in it }
                return null to (err ?: st.optString("error")).removePrefix("error: ")
            }
            Thread.sleep(300)
        }
        return null to "timeout"
    }

    /** A server the app can log in to (set up over SSH, not an imported profile). */
    fun canSsh(server: JSONObject?) = server != null && !server.optBoolean("imported") &&
        (server.optString("key").isNotEmpty() || server.optString("password").isNotEmpty())

    fun run(server: JSONObject, mode: String, env: JSONObject, done: (result: JSONObject?, error: String?) -> Unit) {
        val started = Native.setupStart(JSONObject(server.toString()).put("mode", mode).put("env", env).toString())
        if (Native.isError(started)) { done(null, started.removePrefix("error: ")); return }
        main.post(object : Runnable {
            override fun run() {
                val st = JSONObject(Native.setupStatus())
                if (st.optBoolean("running")) { main.postDelayed(this, 400); return }
                val r = st.optJSONObject("result")
                if (r != null) { done(r, null); return }
                // the script's own "error: …" line is more telling than "exited with status 1"
                val err = st.optString("log").lines().lastOrNull { it.startsWith("error: ") && "script failed" !in it }
                done(null, (err ?: st.optString("error")).removePrefix("error: "))
            }
        })
    }
}
