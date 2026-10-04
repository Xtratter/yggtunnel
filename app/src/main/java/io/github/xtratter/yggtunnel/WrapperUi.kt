package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.text.InputType
import android.widget.LinearLayout
import android.widget.ScrollView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import org.json.JSONObject

/**
 * The wss wrapper (Yggdrasil peering inside HTTPS to the server's own site): set it up or remove it
 * with go/wrapper.sh over SSH, or enter the address by hand; the peer is pinned in the list.
 */
class WrapperUi(private val a: MainActivity) {
    private val prefs = Prefs(a)

    /** wss://<site>/<path>: Yggdrasil peering through the server's own HTTPS site (WebSocket behind its web server). */
    fun edit(s: JSONObject) {
        val old = s.optString("wssPeer")
        val edit = a.field(R.string.wss_hint, old, InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI).apply { typeface = Typeface.MONOSPACE; textSize = 13f }
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.wss_explain) })
            addView(M3Widgets.button(a, a.getString(R.string.wss_auto)) { d.dismiss(); wrapper(s, "setup") }.help(R.string.wss_auto, R.string.wss_auto_hint),
                LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(12f) })
            addView(a.text(12.5f, M3.TEXT3).apply { text = a.getString(R.string.wss_auto_hint); setPadding(0, a.dp(6f), 0, a.dp(8f)) })
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.wss_manual) })
            addView(edit)
            if (old.isNotEmpty()) addView(M3Widgets.button(a, a.getString(R.string.wss_remove), M3Widgets.ButtonKind.DANGER) {
                d.dismiss(); confirmWrapperRemove(s)
            }, LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(8f) })
        }
        d = Theme.dialog(a).setTitle(R.string.wss_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.save, null)
            .setNegativeButton(android.R.string.cancel, null)
            .create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val v = wssWithPort(edit.text.toString().trim())
                if (v == null) { edit.error = a.getString(R.string.wss_bad); return@setOnClickListener }
                saveWss(s, v)
                d.dismiss()
            }
        }
        d.show()
    }

    private fun saveWss(s: JSONObject, v: String) {
        val old = s.optString("wssPeer")
        if (old.isNotEmpty()) prefs.peers = prefs.peerList.filter { it != old }.joinToString("\n")
        prefs.server = s.put("wssPeer", v)
        if (v.isNotEmpty()) prefs.addPeerFirst(v)
        a.refreshAll()
        a.settingsChanged()
    }

    /** wrapper.sh on the server: [action] setup / remove; a domain question is asked and the script run again. */
    private fun wrapper(s: JSONObject, action: String, domain: String = "", new: Boolean = false, telemt: Boolean = false, waitDns: Boolean = false) {
        val env = JSONObject().put("ACTION", action).put("WRAP_IP", s.optString("host"))
        if (waitDns) env.put("WRAP_WAIT_DNS", "1") // a name just created at deSEC
        if (telemt) env.put("WRAP_TELEMT", "1")
        if (domain.isNotEmpty()) env.put("WRAP_DOMAIN", domain)
        if (new) env.put("WRAP_NEW", "1")
        val params = JSONObject(s.toString()).put("mode", "wrapper").put("env", env)
        SetupRunner.run(a, params, R.string.wss_running) { result, _ ->
            when {
                result == null -> false
                result.has("wssPeer") -> { saveWss(s, result.getString("wssPeer")); true }
                result.optBoolean("removed") -> { saveWss(s, ""); true }
                result.optString("need") == "domain" -> {
                    val names = result.getJSONArray("candidates").let { c -> (0 until c.length()).map { c.getString(it) } }
                    a.main.postDelayed({ chooseDomain(s, names) }, 300); true
                }
                result.optString("need") == "telemt" -> { a.main.postDelayed({ askTelemt(s, domain) }, 300); true }
                result.optString("need") == "new-domain" -> { a.main.postDelayed({ askNewDomain(s, result.optString("ip")) }, 300); true }
                else -> false
            }
        }
    }

    /** telemt on 443 cuts long masked connections (5 s idle, 60 s, 5 MB) — ask before changing its config. */
    private fun askTelemt(s: JSONObject, domain: String) {
        val d = Theme.dialog(a).setTitle(R.string.wss_telemt_title).setMessage(R.string.wss_telemt_text)
            .setPositiveButton(R.string.wss_telemt_ok) { _, _ -> wrapper(s, "setup", domain, telemt = true) }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    private fun chooseDomain(s: JSONObject, names: List<String>) {
        val d = Theme.dialog(a).setTitle(R.string.wss_choose)
            .setItems(names.toTypedArray()) { _, i -> wrapper(s, "setup", names[i]) }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    private fun askNewDomain(s: JSONObject, serverIp: String) {
        val ip = serverIp.ifEmpty { s.optString("host") }
        val edit = a.field(R.string.wss_domain_hint, "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.wss_new_explain, ip) })
            addView(edit)
            addView(M3Widgets.button(a, a.getString(R.string.desec_create), M3Widgets.ButtonKind.TONAL) { d.dismiss(); desecDialog(s, ip) }
                .help(R.string.desec_create, R.string.h_desec), LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(12f) })
        }
        d = Theme.dialog(a).setTitle(R.string.wss_new_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.server_setup, null)
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val dom = edit.text.toString().trim().lowercase().removePrefix("https://").trimEnd('/')
                if (!Regex("^[a-z0-9-]+(\\.[a-z0-9-]+)+$").matches(dom)) { edit.error = a.getString(R.string.wss_domain_bad); return@setOnClickListener }
                d.dismiss(); wrapper(s, "setup", dom, new = true)
            }
        }
        d.show()
    }

    /** A name for the site at deSEC (desec.io), made by the server (store.sh ACTION=desec): a new name.dedyn.io
     *  or one under the account's own domains, its A record → [ip]; then the site is set up, waiting for DNS. */
    private fun desecDialog(s: JSONObject, ip: String) {
        val saved = prefs.desecToken != null
        val token = a.field(if (saved) R.string.desec_token_saved else R.string.desec_token_hint, "",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD).apply { typeface = Typeface.MONOSPACE; textSize = 13f; noCopy() }
        val name = a.field(R.string.desec_name_hint, "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        val note = a.text(12.5f, M3.TEXT3)
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.desec_explain, ip) })
            addView(M3Widgets.button(a, a.getString(R.string.desec_open), M3Widgets.ButtonKind.OUTLINED) {
                runCatching { a.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse("https://desec.io/tokens"))) }
            }, LinearLayout.LayoutParams(-1, a.dp(40f)).apply { topMargin = a.dp(8f) })
            add(token, 8f); add(name, 4f); add(note, 6f)
        }
        d = Theme.dialog(a).setTitle(R.string.desec_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.desec_go, null)
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.setOnShowListener {
            val go = d.getButton(AlertDialog.BUTTON_POSITIVE)
            go.setOnClickListener {
                val tok = token.text.toString().trim().ifEmpty { prefs.desecToken.orEmpty() }
                val dom = name.text.toString().trim().lowercase().removePrefix("https://").trimEnd('/', '.')
                if (tok.isEmpty()) { token.error = a.getString(R.string.desec_token_needed); return@setOnClickListener }
                if (!Regex("^[a-z0-9-]+(\\.[a-z0-9-]+)+$").matches(dom)) { name.error = a.getString(R.string.wss_domain_bad); return@setOnClickListener }
                go.isEnabled = false
                note.setTextColor(M3.TEXT3); note.setText(R.string.desec_working)
                val env = JSONObject().put("ACTION", "desec").put("DESEC_OP", "claim").put("DESEC_TOKEN", tok)
                    .put("DESEC_NAME", dom).put("DESEC_IP", ip)
                ServerCall.run(s, "store", env) { r, err ->
                    go.isEnabled = true
                    if (r == null) { note.setTextColor(M3.HOT); note.text = a.getString(R.string.error, err.orEmpty()); return@run }
                    if (token.text.isNotBlank()) prefs.desecToken = tok // kept only once it worked
                    d.dismiss()
                    wrapper(s, "setup", r.optString("domain", dom), new = true, waitDns = true)
                }
            }
        }
        d.show()
    }

    private fun confirmWrapperRemove(s: JSONObject) {
        val d = Theme.dialog(a).setTitle(R.string.wss_remove).setMessage(R.string.wss_remove_text)
            .setPositiveButton(R.string.wss_remove) { _, _ -> wrapper(s, "remove") }
            .setNeutralButton(R.string.wss_forget) { _, _ -> saveWss(s, "") }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    /** Yggdrasil needs an explicit port even for wss ("missing port in address"): add :443 when there is none. "" stays "". */
    private fun wssWithPort(v: String): String? {
        if (v.isEmpty()) return ""
        if (!Regex("^wss://[^/\\s]+/\\S+$").matches(v)) return null
        val u = runCatching { java.net.URI(v) }.getOrNull() ?: return null
        if (u.host == null) return null
        return if (u.port > 0) v else v.replaceFirst("wss://${u.rawAuthority}", "wss://${u.rawAuthority}:443")
    }
}
