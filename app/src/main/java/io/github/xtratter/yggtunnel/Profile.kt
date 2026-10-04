package io.github.xtratter.yggtunnel

import java.util.Base64
import org.json.JSONObject

/**
 * A device profile for another phone: its own WireGuard key (already registered on
 * the server), the server's key and Yggdrasil address, and peers. Shared as
 * yggtunnel://import#<base64url JSON> — a link, a QR code or plain text.
 * Whoever has it can use the tunnel, so it is shown with a warning.
 */
object Profile {
    private const val PREFIX = "yggtunnel://import#"

    // java.util.Base64 (not android.util): the same on the phone and in JVM unit tests
    fun link(p: JSONObject) = PREFIX + Base64.getUrlEncoder().withoutPadding().encodeToString(p.toString().toByteArray())

    /** Finds a profile link in any text (a pasted message, a link) and decodes it, or null. */
    fun parse(text: String): JSONObject? {
        val i = text.indexOf(PREFIX).takeIf { it >= 0 } ?: return null
        val data = text.substring(i + PREFIX.length).takeWhile { it.isLetterOrDigit() || it == '-' || it == '_' }
        return runCatching { JSONObject(String(Base64.getUrlDecoder().decode(data))) }.getOrNull()
            ?.takeIf { it.optInt("v") == 1 && it.has("privateKey") && it.has("serverKey") && it.has("serverYgg") }
    }

    /** Adds an imported profile as a server and makes it active: no SSH details, only what the tunnel needs. */
    fun import(prefs: Prefs, p: JSONObject) {
        val result = JSONObject()
            .put("yggAddress", p.getString("serverYgg")).put("wgPublicKey", p.getString("serverKey"))
            .put("wgPort", p.getInt("port")).put("ipv6", p.optBoolean("ipv6"))
            .put("clientIp4", p.getString("clientIp4")).put("clientIp6", p.optString("clientIp6"))
        val first = prefs.servers.isEmpty()
        // the peers that work for the other phone are a good start on a phone without servers;
        // otherwise this phone's own list stays
        val peers = p.optJSONArray("peers")
        if (first && peers != null && peers.length() > 0) prefs.peers = (0 until peers.length()).joinToString("\n") { peers.getString(it) }
        // a new server in the list, made active (its own peers go first)
        prefs.addServer(JSONObject().put("imported", true).put("host", p.optString("name"))
            .put("clientKey", p.getString("privateKey")).put("directPeer", p.optString("directPeer"))
            .put("wssPeer", p.optString("wssPeer"))
            .put("result", result))
        prefs.fullTunnel = true
    }
}
