package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.View
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Dialog
import io.github.xtratter.uikit.M3Widgets
import org.json.JSONArray
import org.json.JSONObject

/** "Server" card and the setup flow: SSH details → server.sh on the server → saved profile. */
class ServerUi(private val a: MainActivity) {
    private val prefs = Prefs(a)

    /** Fills [box] with the server state and its buttons. */
    fun render(box: LinearLayout) {
        box.removeAllViews()
        val s = prefs.server
        val r = s?.optJSONObject("result")
        if (s == null) {
            box.addView(a.text(14f, M3.TEXT2).apply { text = a.getString(R.string.server_none) })
            box.addView(M3Widgets.button(a, a.getString(R.string.server_add)) { edit(null) }.help(R.string.h_server_add_t, R.string.h_server_add),
                LinearLayout.LayoutParams(-1, a.dp(52f)).apply { topMargin = a.dp(12f) })
            box.addView(M3Widgets.button(a, a.getString(R.string.profile_import), M3Widgets.ButtonKind.OUTLINED) { ProfileUi.paste(a) }.help(R.string.h_import_t, R.string.h_import),
                LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(8f) })
            return
        }
        // several servers: which one is active, switching, adding more
        val all = prefs.servers
        box.addView(a.settingsRow(R.string.servers_title) { chooseServer() }.let { (row, summary) ->
            summary.text = a.getString(R.string.servers_summary, all.indexOfFirst { it.optString("id") == s.optString("id") } + 1, all.size)
            row.help(R.string.h_servers_t, R.string.h_servers)
        })
        val imported = s.optBoolean("imported")
        box.addView(a.text(15f, face = M3.medium).apply {
            text = if (imported) Privacy.mask(a, a.getString(R.string.profile_imported, s.optString("host")), s.optString("host"))
            else Privacy.mask(a, "${s.optString("user")}@${s.optString("host")}:${s.optInt("port")}")
            Privacy.track(this)
        }.help(R.string.h_server_t, R.string.h_server))
        if (r == null) {
            // saved, but the setup has not succeeded yet: the details stay for the next try
            box.addView(a.text(13f, M3.WARN).apply { text = a.getString(R.string.server_not_ready); setPadding(0, a.dp(4f), 0, 0) })
            buttons(box, s, R.string.server_setup)
            return
        }
        val ygg = r.optString("yggAddress")
        box.addView(a.text(13f, M3.primary, Typeface.MONOSPACE).apply {
            text = Privacy.mask(a, ygg); Privacy.track(this); setPadding(0, a.dp(6f), 0, 0); setOnClickListener { a.copy(ygg) }
        }.help(R.string.h_server_addr_t, R.string.h_server_addr))
        box.addView(a.text(13f, M3.TEXT2).apply {
            setPadding(0, a.dp(4f), 0, 0)
            text = Privacy.mask(a, if (imported) a.getString(R.string.server_info_imported, r.optInt("wgPort"), r.optString("clientIp4"),
                a.getString(if (r.optBoolean("ipv6")) R.string.yes else R.string.no))
            else a.getString(R.string.server_info, r.optInt("wgPort"), r.optString("clientIp4"), r.optInt("peersUp"),
                a.getString(if (r.optBoolean("ipv6")) R.string.yes else R.string.no)))
            Privacy.track(this)
        }.help(R.string.h_server_info_t, R.string.h_server_info))
        box.addView(LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; minimumHeight = a.dp(52f); gravity = android.view.Gravity.CENTER_VERTICAL
            background = M3.ripple(a, 16f)
            addView(a.text(16f).apply { text = a.getString(R.string.wss_title) })
            addView(a.text(12.5f, M3.TEXT2, Typeface.MONOSPACE).apply {
                text = Privacy.mask(a, s.optString("wssPeer")).ifEmpty { a.getString(R.string.wss_none) }; Privacy.track(this)
            })
            if (!imported) setOnClickListener { WrapperUi(a).edit(s) }
        }.help(R.string.h_wss_t, R.string.h_wss), LinearLayout.LayoutParams(-1, -2).apply { topMargin = a.dp(8f) })
        box.addView(M3Widgets.switchRow(a, a.getString(R.string.full_tunnel), prefs.fullTunnel) { on ->
            prefs.fullTunnel = on
            a.settingsChanged()
        }.help(R.string.h_full_t, R.string.h_full), LinearLayout.LayoutParams(-1, -2).apply { topMargin = a.dp(4f) })
        if (imported) {
            box.addView(M3Widgets.button(a, a.getString(R.string.server_delete), M3Widgets.ButtonKind.DANGER) { confirmDelete() },
                LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(12f) })
            return
        }
        buttons(box, s, R.string.server_redo)
        box.addView(M3Widgets.button(a, a.getString(R.string.devices), M3Widgets.ButtonKind.OUTLINED) { DevicesUi(a, s).show() }.help(R.string.h_devices_t, R.string.h_devices),
            LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(8f) })
        box.addView(M3Widgets.button(a, a.getString(R.string.panel_title), M3Widgets.ButtonKind.OUTLINED) { ServerPanelUi(a, s).show() }.help(R.string.h_panel_t, R.string.h_panel),
            LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(8f) })
    }

    private fun buttons(box: LinearLayout, s: JSONObject, setupText: Int) {
        val row = LinearLayout(a)
        row.addView(M3Widgets.button(a, a.getString(setupText), M3Widgets.ButtonKind.TONAL) { edit(s) }.apply { isSingleLine = true; setPadding(a.dp(8f), 0, a.dp(8f), 0) }.help(R.string.h_redo_t, R.string.h_redo),
            LinearLayout.LayoutParams(0, a.dp(44f), 1f).apply { marginEnd = a.dp(8f) })
        row.addView(M3Widgets.button(a, a.getString(R.string.server_delete), M3Widgets.ButtonKind.DANGER) { confirmDelete() }.apply { isSingleLine = true }.help(R.string.h_delete_t, R.string.h_delete),
            LinearLayout.LayoutParams(0, a.dp(44f), 1f))
        box.addView(row, LinearLayout.LayoutParams(-1, -2).apply { topMargin = a.dp(12f) })
    }

    private fun label(res: Int) = a.text(12f, M3.TEXT2).apply { text = a.getString(res); setPadding(0, a.dp(8f), 0, 0) }

    /**
     * SSH details dialog; [old] — a saved server (fields prefilled, host key kept). Its key and passphrase are
     * never put back into the fields (nothing to copy out): empty fields keep them, a pasted key replaces them.
     */
    private fun edit(old: JSONObject?) {
        val oldKey = old?.optString("key").orEmpty()
        val oldPass = old?.optString("passphrase").orEmpty()
        val oldPassword = old?.optString("password").orEmpty()
        // a new server: by password (what a fresh VPS comes with); a saved one: how it logs in now
        var byPassword = if (old == null) true else oldKey.isEmpty() && oldPassword.isNotEmpty()
        val host = a.field(R.string.server_host, old?.optString("host").orEmpty(), InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        val port = a.field(R.string.server_port, (old?.optInt("port") ?: 22).toString(), InputType.TYPE_CLASS_NUMBER)
        val user = a.field(R.string.server_user, old?.optString("user") ?: "root")
        val key = a.field(if (oldKey.isEmpty()) R.string.server_key_hint else R.string.server_key_saved, "",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS or InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD).apply {
            typeface = Typeface.MONOSPACE; textSize = 11f; maxLines = 4; setHorizontallyScrolling(true)
            noCopy()
        }
        val pass = a.field(if (oldPass.isEmpty()) R.string.server_pass else R.string.server_pass_saved, "",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)
        val password = a.field(if (oldPassword.isEmpty()) R.string.server_password_hint else R.string.server_password_saved, "",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD).apply { noCopy() }
        val keyBox = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }
        val passwordBox = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }
        val modes = LinearLayout(a).apply { gravity = android.view.Gravity.CENTER_VERTICAL }
        fun renderModes() {
            modes.removeAllViews()
            for ((label, pw) in listOf(a.getString(R.string.server_by_password) to true, a.getString(R.string.server_by_key) to false))
                modes.addView(M3Widgets.chip(a, label, byPassword == pw) { byPassword = pw; renderModes() },
                    LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginEnd = a.dp(6f) })
            keyBox.visibility = if (byPassword) android.view.View.GONE else android.view.View.VISIBLE
            passwordBox.visibility = if (byPassword) android.view.View.VISIBLE else android.view.View.GONE
        }
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { text = a.getString(R.string.server_explain) })
            addView(label(R.string.server_host)); addView(host)
            val hp = LinearLayout(a)
            hp.addView(LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; addView(label(R.string.server_port).help(R.string.h_ssh_port_t, R.string.h_ssh_port)); addView(port) },
                LinearLayout.LayoutParams(0, -2, 1f).apply { marginEnd = a.dp(12f) })
            hp.addView(LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; addView(label(R.string.server_user).help(R.string.h_ssh_user_t, R.string.h_ssh_user)); addView(user) },
                LinearLayout.LayoutParams(0, -2, 2f))
            addView(hp)
            addView(label(R.string.server_login).help(R.string.server_login, R.string.h_server_login)); add(modes, 4f)
            passwordBox.addView(label(R.string.server_password)); passwordBox.addView(password)
            passwordBox.addView(a.text(12f, M3.TEXT3).apply { setText(R.string.server_password_note); setPadding(0, a.dp(4f), 0, 0) })
            addView(passwordBox)
            keyBox.addView(label(R.string.server_key).help(R.string.h_ssh_key_t, R.string.h_ssh_key)); keyBox.addView(key)
            keyBox.addView(M3Widgets.button(a, a.getString(R.string.server_key_file), M3Widgets.ButtonKind.OUTLINED) {
                a.pickFile { text -> key.setText(text.trim()) }
            }, LinearLayout.LayoutParams(-1, a.dp(44f)).apply { topMargin = a.dp(6f) })
            keyBox.addView(label(R.string.server_pass)); keyBox.addView(pass)
            addView(keyBox)
            renderModes()
        }
        val d = Theme.dialog(a).setTitle(R.string.server_title).setView(ScrollView(a).apply { addView(box) })
            .setPositiveButton(R.string.server_setup, null)
            .setNegativeButton(android.R.string.cancel, null)
            .apply { if (old != null) setNeutralButton(R.string.save, null) }
            .create()
        d.prestyle()
        /** Checks the fields; null (and the bad field marked) if something is missing. */
        fun profile(): JSONObject? {
            val h = host.text.toString().trim().removePrefix("[").removeSuffix("]")
            val pt = port.text.toString().toIntOrNull()
            val err = when {
                h.isEmpty() -> host
                pt == null || pt !in 1..65535 -> port
                user.text.isBlank() -> user
                byPassword -> if (password.text.isEmpty() && oldPassword.isEmpty()) password else null
                key.text.isBlank() && oldKey.isNotEmpty() -> null // the saved key stays
                !key.text.contains("PRIVATE KEY") -> key
                else -> null
            }
            if (err != null) { err.error = a.getString(R.string.server_field_error); err.requestFocus(); return null }
            val newKey = !byPassword && key.text.isNotBlank()
            val profile = JSONObject()
                .put("host", h).put("port", pt).put("user", user.text.toString().trim())
            if (byPassword) {
                // by password: no key until the setup adds the app's own (go/core/setup.go installKey)
                profile.put("password", password.text.toString().ifEmpty { oldPassword })
            } else {
                profile.put("key", if (newKey) key.text.toString().trim() else oldKey)
                    // a new key takes the passphrase as typed; with the old key an empty field keeps the old one
                    .put("passphrase", pass.text.toString().ifEmpty { if (newKey) "" else oldPass })
                // a user other than root may need the password for sudo
                if (oldPassword.isNotEmpty() && user.text.toString().trim() != "root") profile.put("password", oldPassword)
            }
            profile
                .put("hostKey", if (old?.optString("host") == h && old.optInt("port") == pt) old.optString("hostKey") else "")
            // changing the details keeps the server's id, its wrapper and the last good result
            // (the tunnel keeps working if the new setup fails)
            if (old != null) for (k in listOf("id", "wssPeer", "result", "speed")) old.opt(k)?.let { profile.put(k, it) }
            return profile
        }
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_NEUTRAL)?.setOnClickListener {
                // only saves the details; the server is not touched
                val profile = profile() ?: return@setOnClickListener
                prefs.updateServer(profile)
                a.refreshAll()
                d.dismiss()
            }
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val profile = profile() ?: return@setOnClickListener
                // keep the details whatever the setup ends with; a server set up becomes the active one
                if (old == null) prefs.addServer(profile) else {
                    prefs.updateServer(profile)
                    if (prefs.server?.optString("id") != profile.optString("id")) { prefs.switchServer(profile.getString("id")); a.settingsChanged() }
                }
                a.refreshAll()
                d.dismiss()
                run(profile)
            }
        }
        d.show()
    }

    /** Full setup: server.sh; saves the profile on success. */
    private fun run(profile: JSONObject) {
        val params = JSONObject(profile.toString())
            .put("clientPub", prefs.wgKeys.getString("public"))
            .put("wgPort", Prefs.WG_PORT).put("yggPort", Prefs.YGG_PORT)
            .put("peers", JSONArray(Prefs.DEFAULT_PEERS))
            .put("env", JSONObject().put("CLIENT_NAME", DevicesUi.thisPhoneName()))
        SetupRunner.run(a, params, R.string.server_running) { result, hostKey ->
            if (result != null) {
                profile.put("hostKey", hostKey).put("result", result)
                // set up by password: from now on the app's own key; root needs no password any more
                SetupRunner.lastSshKey.ifEmpty { null }?.let { k ->
                    profile.put("key", k).put("passphrase", "")
                    if (profile.optString("user") == "root") profile.remove("password")
                }
                prefs.server = profile
                prefs.addPeerFirst(Prefs.directPeer(profile.getString("host"), result.optInt("yggPort")))
                a.settingsChanged()
            } else if (hostKey.isNotEmpty() && profile.optString("hostKey").isEmpty()) {
                // the server key is remembered even after a failed setup (trust on first use)
                profile.put("hostKey", hostKey); prefs.server = profile
            }
            result != null
        }
    }

    /** The list of servers: tap one to make it active; add another or import a profile. */
    private fun chooseServer() {
        val all = prefs.servers
        val active = prefs.server?.optString("id")
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        for (srv in all) box.addView(LinearLayout(a).apply {
            gravity = android.view.Gravity.CENTER_VERTICAL
            addView(android.widget.RadioButton(a).apply {
                textSize = 15f; setTextColor(M3.TEXT); minHeight = a.dp(46f)
                val host = srv.optString("host")
                text = Privacy.mask(a, if (srv.optBoolean("imported")) a.getString(R.string.profile_imported, host) else host, host)
                isChecked = srv.optString("id") == active; M3Widgets.tint(this)
                setOnClickListener {
                    if (srv.optString("id") != active) { prefs.switchServer(srv.optString("id")); a.refreshAll(); a.settingsChanged() }
                    d.dismiss()
                }
            }, LinearLayout.LayoutParams(0, -2, 1f))
            // an imported profile has no SSH details to change
            if (!srv.optBoolean("imported")) addView(M3Widgets.button(a, a.getString(R.string.edit), M3Widgets.ButtonKind.OUTLINED) { d.dismiss(); edit(srv) }
                .apply { isSingleLine = true; textSize = 13f; setPadding(a.dp(14f), 0, a.dp(14f), 0) },
                LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginStart = a.dp(8f) })
            addView(M3Widgets.button(a, "✕", M3Widgets.ButtonKind.DANGER) { d.dismiss(); confirmDelete(srv) }
                .apply { isSingleLine = true; textSize = 13f; setPadding(a.dp(12f), 0, a.dp(12f), 0) }.help(R.string.h_delete_t, R.string.h_delete),
                LinearLayout.LayoutParams(-2, a.dp(36f)).apply { marginStart = a.dp(6f) })
        })
        box.addView(M3Widgets.button(a, a.getString(R.string.server_add_more), M3Widgets.ButtonKind.TONAL) { d.dismiss(); edit(null) },
            LinearLayout.LayoutParams(-1, a.dp(46f)).apply { topMargin = a.dp(10f) })
        box.addView(M3Widgets.button(a, a.getString(R.string.profile_import), M3Widgets.ButtonKind.OUTLINED) { d.dismiss(); ProfileUi.paste(a) },
            LinearLayout.LayoutParams(-1, a.dp(46f)).apply { topMargin = a.dp(8f) })
        d = Theme.dialog(a).setTitle(R.string.servers_title).setView(a.bounded(android.widget.ScrollView(a).apply { addView(box) }))
            .setNegativeButton(R.string.close, null).create()
        d.prestyle(); d.show()
    }

    /** Forgets [srv] on this phone (null — the active one); the server itself is not touched. */
    private fun confirmDelete(srv: JSONObject? = null) {
        val d = Theme.dialog(a).setTitle(R.string.server_delete)
            .setMessage(a.getString(R.string.server_delete_text) + (srv?.let { "\n\n" + it.optString("host") } ?: ""))
            .setPositiveButton(R.string.server_delete) { _, _ ->
                if (srv == null || srv.optString("id") == prefs.server?.optString("id"))
                    prefs.server = null // its own peers leave the list; the next server (if any) becomes active
                else prefs.servers = prefs.servers.filter { it.optString("id") != srv.optString("id") }
                a.refreshAll()
                a.settingsChanged()
            }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }
}
