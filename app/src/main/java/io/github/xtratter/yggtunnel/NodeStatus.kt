package io.github.xtratter.yggtunnel

import org.json.JSONObject

/** The node's state (Native.status JSON) parsed once per refresh. */
class NodeStatus private constructor(json: JSONObject?) {
    val address: String = json?.optString("address").orEmpty()
    val peers: List<JSONObject> = json?.optJSONArray("peers")?.let { a -> (0 until a.length()).map { a.getJSONObject(it) } } ?: emptyList()
    val up = peers.count { it.optBoolean("up") }
    val routing = json?.optLong("routing") ?: 0
    val uptime = json?.optDouble("uptime") ?: 0.0
    val reserve: Set<String> = json?.optJSONArray("reserve")?.let { r -> (0 until r.length()).map { r.getString(it) }.toSet() } ?: emptySet()
    /** WireGuard to the server, null in the Yggdrasil-only mode. */
    val tunnel: JSONObject? = json?.optJSONObject("tunnel")
    /** WireGuard renews the handshake every 2 minutes while traffic flows; 3 minutes without one — not connected. */
    val shaken = tunnel?.optDouble("handshakeAgo", -1.0)?.let { it in 0.0..180.0 } ?: false

    /** Really connected: the service is on, some peer is up and (with a server) WireGuard has shaken hands. */
    fun ok(state: YggVpnService.State) = state == YggVpnService.State.ON && up > 0 && (tunnel == null || shaken)

    companion object {
        fun read() = NodeStatus(runCatching { JSONObject(Native.status()) }.getOrNull())
    }
}
