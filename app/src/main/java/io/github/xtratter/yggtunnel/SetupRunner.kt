package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.ScrollView
import io.github.xtratter.uikit.M3
import org.json.JSONObject

/** Runs one of the embedded server scripts over SSH (go/setup.go) with a live log in a dialog. */
object SetupRunner {
    /** After a full setup by password: the app's own SSH key the server now accepts ("" — none). */
    @Volatile var lastSshKey = ""

    /** Runs a setup on the server with a live log; [done] gets the result (null — failed) and the server key, returns success. */
    fun run(a: MainActivity, params: JSONObject, title: Int, done: (JSONObject?, String) -> Boolean) {
        val started = Native.setupStart(params.toString())
        val log = a.text(11f, M3.TEXT, Typeface.MONOSPACE).apply { setTextIsSelectable(true); setPadding(a.dp(20f), 0, a.dp(20f), 0) }
        val scroll = ScrollView(a).apply { addView(log) }
        val d = Theme.dialog(a).setTitle(title).setView(a.bounded(scroll))
            .setPositiveButton(R.string.close, null)
            .setNeutralButton(R.string.copy) { _, _ -> a.copy(log.text.toString()) }
            .setCancelable(false)
            .create()
        val main = Handler(Looper.getMainLooper())
        val poll = object : Runnable {
            override fun run() {
                val st = JSONObject(Native.setupStatus())
                if (log.text.length != st.optString("log").length) {
                    log.text = st.optString("log")
                    scroll.post { scroll.fullScroll(View.FOCUS_DOWN) }
                }
                if (st.optBoolean("running")) { main.postDelayed(this, 500); return }
                d.getButton(AlertDialog.BUTTON_POSITIVE).visibility = View.VISIBLE
                lastSshKey = st.optString("sshKey")
                val ok = done(st.optJSONObject("result"), st.optString("hostKey"))
                // a result that asks for more (a domain, telemt, a new site) is not finished: the next dialog asks
                val asks = st.optJSONObject("result")?.has("need") == true
                d.setTitle(if (ok && asks) R.string.setup_needs_answer else if (ok) R.string.server_done else R.string.server_failed)
                a.refreshAll()
            }
        }
        d.prestyle()
        d.setOnShowListener {
            if (Native.isError(started)) { log.text = started; d.setTitle(R.string.server_failed); return@setOnShowListener }
            d.getButton(AlertDialog.BUTTON_POSITIVE).visibility = View.INVISIBLE
            main.post(poll)
        }
        d.show()
    }
}
