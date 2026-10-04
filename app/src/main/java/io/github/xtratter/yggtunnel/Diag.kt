package io.github.xtratter.yggtunnel

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import org.json.JSONObject
import java.io.File

/**
 * Upload diagnostics: records the link's TCP state (go/tcpdiag.go) for two minutes while the user runs a
 * speed test, then keeps the log in files/diag.txt, notes it in the connection log and sends it to the
 * server (store.sh ACTION=diag → /etc/yggtunnel/diag/), where it can be read over SSH.
 */
object Diag {
    const val SECONDS = 120
    /** The congestion control set on the live link this session (null — the system default). */
    @Volatile var cc: String? = null

    /** Sends a report to the server (/etc/yggtunnel/diag/<phone>[-kind]-<time>.log); blocking — call off the main thread. */
    fun upload(app: Context, kind: String, text: String): String {
        val srv = Prefs(app).server
        if (srv == null || !ServerCall.canSsh(srv)) return app.getString(R.string.diag_saved_local)
        val phone = DevicesUi.thisPhoneName().replace(Regex("[^A-Za-z0-9._-]+"), "-").trim('-').take(50).ifEmpty { "phone" }
        val name = if (kind.isEmpty()) phone else "$phone-$kind"
        val b64 = android.util.Base64.encodeToString(text.toByteArray(), android.util.Base64.NO_WRAP)
        val (res, err) = ServerCall.runBlocking(srv, "store", JSONObject().put("ACTION", "diag").put("NAME", name).put("DATA_B64", b64))
        return if (res != null) app.getString(R.string.diag_sent, res.optString("saved")) else app.getString(R.string.diag_send_failed, err.orEmpty())
    }

    fun start(c: Context) {
        val app = c.applicationContext
        val r = Native.diagStart(SECONDS)
        if (Native.isError(r)) { Toast.makeText(app, app.getString(R.string.error, r.removePrefix("error: ")), Toast.LENGTH_LONG).show(); return }
        ConnLog.write(app, "${ConnLog.INFO} " + app.getString(R.string.diag_started))
        Thread {
            while (JSONObject(Native.diagStatus()).optBoolean("running")) Thread.sleep(1000)
            val text = Native.diagText()
            File(app.filesDir, "diag.txt").writeText(text)
            val msg = upload(app, "", text)
            ConnLog.write(app, "${ConnLog.INFO} $msg")
            Handler(Looper.getMainLooper()).post { Toast.makeText(app, msg, Toast.LENGTH_LONG).show() }
        }.start()
    }
}
