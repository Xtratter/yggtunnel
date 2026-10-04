package io.github.xtratter.yggtunnel

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class ServerStripTest {
    @Test fun noSshKeysLeave() {
        val servers = JSONArray().put(JSONObject().put("id", "a").put("host", "h").put("key", "-----BEGIN OPENSSH PRIVATE KEY-----").put("passphrase", "pw").put("password", "root-password-123"))
        val backup = JSONObject().put("app", "yggtunnel").put("prefs", JSONObject()
            .put("servers", JSONObject().put("t", "s").put("v", servers.toString()))
            .put("theme", JSONObject().put("t", "s").put("v", "DARK"))
            .put("desec_token", JSONObject().put("t", "s").put("v", "secret-desec-token")))
        val out = Server.strip(backup).toString()
        assertFalse(out.contains("PRIVATE KEY"))
        assertFalse(out.contains("passphrase"))
        assertFalse(out.contains("secret-desec-token"))
        assertFalse(out.contains("root-password-123"))
        val s = JSONArray(JSONObject(out).getJSONObject("prefs").getJSONObject("servers").getString("v")).getJSONObject(0)
        assertEquals("h", s.getString("host"))
    }
}
