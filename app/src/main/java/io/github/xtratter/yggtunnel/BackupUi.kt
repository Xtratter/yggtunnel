package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.text.InputType
import android.view.View
import android.widget.LinearLayout
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** Settings → Backup of settings: save everything to a password-protected file, or restore from one. */
class BackupUi(private val a: MainActivity) {
    fun show() {
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            addView(a.text(13f, M3.TEXT2).apply { setText(R.string.backup_explain) })
            addView(M3Widgets.button(a, a.getString(R.string.backup_create)) { d.dismiss(); create() }
                .help(R.string.backup_create, R.string.h_backup_create), LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(12f) })
            addView(M3Widgets.button(a, a.getString(R.string.backup_restore), M3Widgets.ButtonKind.OUTLINED) { d.dismiss(); a.pickBytes(::restore) }
                .help(R.string.backup_restore, R.string.h_backup_restore), LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(8f) })
            // a plain copy on the active server (/etc/yggtunnel/clients), without the SSH keys
            val srv = Prefs(a).server
            if (ServerCall.canSsh(srv)) {
                add(a.text(13f, M3.TEXT2).apply { text = Privacy.mask(a, a.getString(R.string.srvcfg_explain, srv!!.optString("host")), srv.optString("host")) }, 16f)
                addView(M3Widgets.button(a, a.getString(R.string.srvcfg_save), M3Widgets.ButtonKind.TONAL) { d.dismiss(); saveToServer() }
                    .help(R.string.srvcfg_save, R.string.h_srvcfg), LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(8f) })
                addView(M3Widgets.button(a, a.getString(R.string.srvcfg_restore), M3Widgets.ButtonKind.OUTLINED) { d.dismiss(); listOnServer() }
                    .help(R.string.srvcfg_restore, R.string.h_srvcfg), LinearLayout.LayoutParams(-1, a.dp(48f)).apply { topMargin = a.dp(8f) })
            }
        }
        d = Theme.dialog(a).setTitle(R.string.backup_title).setView(box).setNegativeButton(R.string.close, null).create()
        d.prestyle(); d.show()
    }

    private fun password(hint: Int) = a.field(hint, "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)

    /** Asks for a password twice, then encrypts in the background (PBKDF2 takes a second or two) and asks where to save. */
    private fun create() {
        val p1 = password(R.string.backup_password)
        val p2 = password(R.string.backup_password_again)
        val note = a.text(12.5f, M3.WARN).apply { setText(R.string.backup_password_note) }
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0); addView(p1); addView(p2); add(note, 8f) }
        val d = Theme.dialog(a).setTitle(R.string.backup_create).setView(box)
            .setPositiveButton(R.string.backup_create, null).setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.setOnShowListener {
            val ok = d.getButton(AlertDialog.BUTTON_POSITIVE)
            ok.setOnClickListener {
                val pw = p1.text.toString()
                when {
                    pw.length < Backup.MIN_PASSWORD -> { p1.error = a.getString(R.string.backup_password_short, Backup.MIN_PASSWORD); return@setOnClickListener }
                    pw != p2.text.toString() -> { p2.error = a.getString(R.string.backup_password_mismatch); return@setOnClickListener }
                }
                ok.isEnabled = false; note.setTextColor(M3.TEXT2); note.setText(R.string.backup_working)
                val json = Prefs(a).exportAll().toString()
                Thread {
                    val bytes = runCatching { Backup.seal(json, pw.toCharArray()) }
                    a.main.post {
                        d.dismiss()
                        bytes.onSuccess { a.saveFile("yggtunnel-" + SimpleDateFormat("yyyy-MM-dd", Locale.ROOT).format(Date()) + ".ygtb", it) }
                            .onFailure { android.widget.Toast.makeText(a, it.message, android.widget.Toast.LENGTH_LONG).show() }
                    }
                }.start()
            }
        }
        d.show()
    }

    /** Asks the file's password, decrypts in the background, shows what is inside and replaces the settings on confirm. */
    private fun restore(data: ByteArray) {
        val pw = password(R.string.backup_password)
        val note = a.text(12.5f, M3.TEXT2).apply { setText(R.string.backup_restore_note) }
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0); addView(pw); add(note, 8f) }
        val d = Theme.dialog(a).setTitle(R.string.backup_restore).setView(box)
            .setPositiveButton(R.string.backup_open, null).setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.setOnShowListener {
            val ok = d.getButton(AlertDialog.BUTTON_POSITIVE)
            ok.setOnClickListener {
                ok.isEnabled = false; note.setText(R.string.backup_working)
                val chars = pw.text.toString().toCharArray()
                Thread {
                    val r = runCatching { JSONObject(Backup.open(data, chars)) }
                    a.main.post {
                        ok.isEnabled = true
                        r.onSuccess { json -> d.dismiss(); confirm(json) }.onFailure { e ->
                            note.setTextColor(M3.HOT)
                            note.setText(when (e) { is Backup.NotABackup -> R.string.backup_not_ours; is Backup.BadPassword -> R.string.backup_bad_password; else -> R.string.backup_bad_password })
                        }
                    }
                }.start()
            }
        }
        d.show()
    }

    // ---- a plain copy on the server ----

    /** This phone's name for its file on the server: letters, digits, dot, dash. */
    private fun fileName() = DevicesUi.thisPhoneName().replace(Regex("[^A-Za-z0-9._-]+"), "-").trim('-').take(64).ifEmpty { "phone" }

    private fun saveToServer() {
        val srv = Prefs(a).server ?: return
        val cfg = Server.strip(Prefs(a).exportAll())
        val b64 = android.util.Base64.encodeToString(cfg.toString().toByteArray(), android.util.Base64.NO_WRAP)
        android.widget.Toast.makeText(a, R.string.srvcfg_saving, android.widget.Toast.LENGTH_SHORT).show()
        ServerCall.run(srv, "store", JSONObject().put("ACTION", "put").put("NAME", fileName()).put("CFG_B64", b64)) { r, err ->
            android.widget.Toast.makeText(a, if (r != null) a.getString(R.string.srvcfg_saved, fileName()) else a.getString(R.string.error, err.orEmpty()),
                android.widget.Toast.LENGTH_LONG).show()
        }
    }

    private fun listOnServer() {
        val srv = Prefs(a).server ?: return
        ServerCall.run(srv, "store", JSONObject().put("ACTION", "list")) { r, err ->
            val items = r?.optJSONArray("clients")
            if (items == null || items.length() == 0) {
                android.widget.Toast.makeText(a, if (r == null) a.getString(R.string.error, err.orEmpty()) else a.getString(R.string.srvcfg_none), android.widget.Toast.LENGTH_LONG).show()
                return@run
            }
            val names = (0 until items.length()).map { items.getJSONObject(it) }
            val labels = names.map { it.optString("name") + " · " + java.text.DateFormat.getDateTimeInstance().format(Date(it.optLong("created"))) }.toTypedArray()
            Theme.dialog(a).setTitle(R.string.srvcfg_restore)
                .setItems(labels) { _, i ->
                    ServerCall.run(srv, "store", JSONObject().put("ACTION", "get").put("NAME", names[i].optString("name"))) { g, e ->
                        val cfg = g?.optJSONObject("config")
                        if (cfg == null) android.widget.Toast.makeText(a, a.getString(R.string.error, e.orEmpty()), android.widget.Toast.LENGTH_LONG).show()
                        else confirm(cfg)
                    }
                }
                .setNegativeButton(android.R.string.cancel, null).create().apply { prestyle(); show() }
        }
    }

    /** Server profiles in a backup: the first host (0.12+ keeps a list, older ones one profile). */
    private fun firstHost(prefs: JSONObject): String {
        prefs.optJSONObject("servers")?.optString("v")?.let { v -> runCatching { org.json.JSONArray(v).getJSONObject(0).optString("host") }.getOrNull() }?.let { return it }
        return prefs.optJSONObject("server")?.optString("v")?.let { runCatching { JSONObject(it).optString("host") }.getOrNull() }.orEmpty()
    }

    private fun confirm(json: JSONObject) {
        val prefs = json.optJSONObject("prefs") ?: JSONObject()
        val server = firstHost(prefs)
        val date = java.text.DateFormat.getDateTimeInstance().format(Date(json.optLong("created")))
        Theme.dialog(a).setTitle(R.string.backup_restore)
            .setMessage(Privacy.mask(a, a.getString(R.string.backup_confirm, date, server.ifEmpty { "—" }), server).toString())
            .setPositiveButton(R.string.backup_replace) { _, _ ->
                val keys = Prefs(a).servers // SSH keys this phone has: a copy from the server comes without them
                runCatching { Prefs(a).importAll(json); Server.restoreKeys(Prefs(a), keys) }.onSuccess {
                    Privacy.load(a)
                    a.rebuild()
                    a.settingsChanged()
                    android.widget.Toast.makeText(a, R.string.backup_restored, android.widget.Toast.LENGTH_LONG).show()
                }.onFailure { android.widget.Toast.makeText(a, it.message, android.widget.Toast.LENGTH_LONG).show() }
            }
            .setNegativeButton(android.R.string.cancel, null).create().apply { prestyle(); show() }
    }
}
