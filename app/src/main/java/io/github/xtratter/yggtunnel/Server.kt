package io.github.xtratter.yggtunnel

import org.json.JSONArray
import org.json.JSONObject

/** Helpers for the settings copy kept on the server: it never carries SSH keys. */
object Server {
    private val SSH = listOf("key", "passphrase", "password")

    /** [backup] (Prefs.exportAll) without the SSH key and passphrase of every server. */
    fun strip(backup: JSONObject): JSONObject {
        val prefs = backup.optJSONObject("prefs") ?: return backup
        prefs.optJSONObject("servers")?.let { o ->
            val list = JSONArray(o.getString("v"))
            for (i in 0 until list.length()) SSH.forEach { list.getJSONObject(i).remove(it) }
            o.put("v", list.toString())
        }
        prefs.optJSONObject("server")?.let { o -> o.put("v", JSONObject(o.getString("v")).apply { SSH.forEach { remove(it) } }.toString()) }
        prefs.remove("desec_token") // the copy on the server is plain JSON
        return backup
    }

    /** After restoring a copy without keys: each server gets back the key this phone had for it (same id, or host and port). */
    fun restoreKeys(prefs: Prefs, before: List<JSONObject>) {
        var changed = false
        val list = prefs.servers.map { s ->
            if (s.optString("key").isNotEmpty()) return@map s
            val old = before.firstOrNull { it.optString("id") == s.optString("id") }
                ?: before.firstOrNull { it.optString("host") == s.optString("host") && it.optInt("port") == s.optInt("port") }
            if (old != null && (old.optString("key").isNotEmpty() || old.optString("password").isNotEmpty())) { SSH.forEach { k -> s.put(k, old.optString(k)) }; changed = true }
            s
        }
        if (changed) prefs.servers = list
    }
}
