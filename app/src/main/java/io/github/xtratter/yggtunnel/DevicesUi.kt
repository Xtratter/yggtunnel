package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.os.Build
import android.text.InputType
import android.util.Base64
import android.view.View
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Dialog
import io.github.xtratter.uikit.M3Surface
import org.json.JSONArray
import org.json.JSONObject

/**
 * Device admin: the server's WireGuard devices (go/devices.sh over SSH) — names, addresses,
 * last handshake, traffic; add, rename, remove, show a device's QR code again. The devices'
 * private keys are kept on the server, so any admin phone can show any profile.
 */
class DevicesUi(private val a: MainActivity, private val server: JSONObject) {
    private val prefs = Prefs(a)
    private lateinit var list: LinearLayout
    private lateinit var status: TextView
    private var busy = false

    fun show() {
        status = a.text(13f, M3.TEXT2).apply { setPadding(a.dp(20f), 0, a.dp(20f), a.dp(8f)) }
        list = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(16f), 0, a.dp(16f), 0) }
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; addView(status); addView(list) }
        val d = Theme.dialog(a).setTitle(R.string.devices).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setPositiveButton(R.string.device_add, null)
            .setNeutralButton(R.string.refresh, null)
            .setNegativeButton(R.string.close, null)
            .create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_POSITIVE).help(R.string.h_dev_add_t, R.string.h_dev_add)
            d.getButton(AlertDialog.BUTTON_NEUTRAL).help(R.string.h_dev_refresh_t, R.string.h_dev_refresh)
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener { askName(a.getString(R.string.device_new_name, nextNumber()), R.string.device_add) { add(it) } }
            d.getButton(AlertDialog.BUTTON_NEUTRAL).setOnClickListener { op("list") }
            op("list")
        }
        d.show()
    }

    private var devices = JSONArray()
    private fun nextNumber() = devices.length() + 1

    /** Runs devices.sh with [action]; the list is redrawn from its result. */
    private fun op(action: String, pub: String = "", name: String? = null, priv: String? = null, then: ((JSONObject?) -> Unit)? = null) {
        if (busy) return
        busy = true
        status.text = a.getString(R.string.devices_loading); status.setTextColor(M3.TEXT2)
        val env = JSONObject().put("ACTION", action)
        if (pub.isNotEmpty()) env.put("CLIENT_PUB", pub)
        if (name != null) env.put("NAME_B64", Base64.encodeToString(name.toByteArray(), Base64.NO_WRAP))
        if (priv != null) env.put("CLIENT_PRIV", priv)
        ServerCall.run(server, "devices", env) { r, err ->
            busy = false
            if (r == null) { fail(err.orEmpty()); then?.invoke(null); return@run }
            devices = r.optJSONArray("devices") ?: JSONArray()
            status.text = a.getString(R.string.devices_count, devices.length())
            render()
            if (then != null) then(r) else uploadKnownKeys()
        }
    }

    /**
     * Profiles the server does not have yet but this phone knows — its own key and devices added
     * by 0.5 (kept here then) — are uploaded one by one.
     */
    private fun uploadKnownKeys() {
        val known = prefs.localDevices + (prefs.wgKeys.getString("public") to prefs.wgKeys.getString("private"))
        val todo = (0 until devices.length()).map { devices.getJSONObject(it) }
            .firstOrNull { !it.optBoolean("hasKey") && it.getString("pub") in known } ?: return
        val pub = todo.getString("pub")
        op("store-key", pub, priv = known.getValue(pub)) { r ->
            if (r != null) { prefs.localDevices = prefs.localDevices - pub; uploadKnownKeys() }
        }
    }

    private fun fail(msg: String) {
        status.text = a.getString(R.string.error, msg); status.setTextColor(M3.HOT)
    }

    private fun render() {
        list.removeAllViews()
        val me = prefs.wgKeys.getString("public")
        for (i in 0 until devices.length()) {
            val dv = devices.getJSONObject(i)
            val pub = dv.getString("pub")
            val name = dv.optString("name").ifEmpty { a.getString(R.string.device_unnamed, dv.optInt("n")) }
            val ago = dv.optDouble("handshakeAgo", -1.0)
            val online = ago in 0.0..180.0
            val row = LinearLayout(a).apply {
                orientation = LinearLayout.VERTICAL
                background = M3Surface(a, 20f)
                setPadding(a.dp(16f), a.dp(12f), a.dp(16f), a.dp(12f))
                foreground = M3.ripple(a, 20f)
                addView(a.text(16f, face = M3.medium).apply {
                    text = android.text.TextUtils.concat(if (online) "● " else "○ ", Privacy.mask(a, name, name)); Privacy.track(this)
                    setTextColor(if (online) M3.OK else M3.TEXT)
                })
                val tags = listOfNotNull(
                    a.getString(R.string.device_this).takeIf { pub == me },
                    a.getString(R.string.device_saved).takeIf { dv.optBoolean("hasKey") },
                )
                addView(a.text(12.5f, M3.TEXT2).apply {
                    setPadding(0, a.dp(4f), 0, 0)
                    text = listOf(
                        dv.optString("ip4"),
                        when { ago < 0 -> a.getString(R.string.device_never); else -> a.getString(R.string.device_seen, ago(ago.toLong())) },
                        "↓ ${Format.bytes(dv.optLong("tx"))} · ↑ ${Format.bytes(dv.optLong("rx"))}",
                    ).joinToString(" · ").let { Privacy.mask(a, it) }.let { info ->
                        if (tags.isEmpty()) info else android.text.TextUtils.concat(info, "\n" + tags.joinToString(" · "))
                    }
                    Privacy.track(this)
                })
                setOnClickListener { actions(dv, name, pub == me) }
                help(R.string.h_dev_row_t, R.string.h_dev_row)
            }
            list.addView(row, LinearLayout.LayoutParams(-1, -2).apply { bottomMargin = a.dp(8f) })
        }
    }

    private fun ago(s: Long) = when {
        s < 90 -> a.getString(R.string.ago_s, s)
        s < 5400 -> a.getString(R.string.ago_m, s / 60)
        s < 129600 -> a.getString(R.string.ago_h, s / 3600)
        else -> a.getString(R.string.ago_d, s / 86400)
    }

    private fun actions(dv: JSONObject, name: String, isMe: Boolean) {
        val pub = dv.getString("pub")
        val items = mutableListOf(a.getString(R.string.device_rename) to { askName(dv.optString("name"), R.string.save) { op("rename", pub, it) } })
        if (dv.optBoolean("hasKey")) items += a.getString(R.string.device_profile) to {
            op("export", pub) { r -> r?.optString("privateKey")?.ifEmpty { null }?.let { showProfile(dv, name, it, isMe) } }
        }
        items += a.getString(R.string.device_copy_key) to { a.copy(pub) }
        items += a.getString(R.string.device_remove) to { confirmRemove(pub, name, isMe) }
        val d = Theme.dialog(a).setTitle(name)
            .setItems(items.map { it.first }.toTypedArray()) { _, i -> items[i].second() }
            .setNegativeButton(R.string.close, null).create()
        d.prestyle()
        d.show()
    }

    private fun askName(value: String, ok: Int, done: (String) -> Unit) {
        val edit = EditText(a).apply {
            setText(value); setSelection(text.length); setSingleLine(); inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
            setTextColor(M3.TEXT); hint = a.getString(R.string.device_name)
        }
        val box = LinearLayout(a).apply { setPadding(a.dp(20f), 0, a.dp(20f), 0); addView(edit, LinearLayout.LayoutParams(-1, -2)) }
        val d = Theme.dialog(a).setTitle(R.string.device_name).setView(box)
            .setPositiveButton(ok) { _, _ -> done(edit.text.toString().trim()) }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    private fun add(name: String) {
        val keys = JSONObject(Native.wgKeyPair())
        val pub = keys.getString("public")
        op("add", pub, name, keys.getString("private")) { r ->
            if (r == null) return@op
            devices.let { arr -> (0 until arr.length()).map { arr.getJSONObject(it) }.firstOrNull { it.getString("pub") == pub } }
                ?.let { showProfile(it, name, keys.getString("private")) }
        }
    }

    private fun showProfile(dv: JSONObject, name: String, privateKey: String, isMe: Boolean = false) {
        val r = server.getJSONObject("result")
        val n = dv.optInt("n")
        val shared = JSONObject()
            .put("v", 1).put("name", server.optString("host"))
            .put("privateKey", privateKey)
            .put("serverKey", r.getString("wgPublicKey")).put("serverYgg", r.getString("yggAddress"))
            .put("port", r.getInt("wgPort")).put("ipv6", r.optBoolean("ipv6"))
            .put("clientIp4", "10.66.66.$n").put("clientIp6", "fd66:66::$n")
            .put("directPeer", Prefs.directPeerOf(server) ?: "")
            .put("wssPeer", server.optString("wssPeer"))
            .put("peers", JSONArray(prefs.effectivePeers))
        ProfileUi.show(a, Profile.link(shared), name, if (isMe) a.getString(R.string.device_profile_me) else null)
    }

    private fun confirmRemove(pub: String, name: String, isMe: Boolean) {
        val d = Theme.dialog(a).setTitle(a.getString(R.string.device_remove_title, name))
            .setMessage(if (isMe) R.string.device_remove_me else R.string.device_remove_text)
            .setPositiveButton(R.string.device_remove) { _, _ ->
                op("remove", pub) { r -> if (r != null) prefs.localDevices = prefs.localDevices - pub }
            }
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    companion object {
        /** A default name for this phone when the server is set up. */
        fun thisPhoneName() = "${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} ${Build.MODEL}".trim()
    }
}
