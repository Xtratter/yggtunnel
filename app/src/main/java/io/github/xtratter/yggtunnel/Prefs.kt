package io.github.xtratter.yggtunnel

import android.content.Context
import org.json.JSONObject

private val WHITESPACE = Regex("\\s+")

/** Settings: the node config (private key → permanent address) and the peer list. */
class Prefs(ctx: Context) {
    private val p = ctx.getSharedPreferences("prefs", Context.MODE_PRIVATE)

    /** A secret value, decrypted (SecretBox); a plain value from an older version is encrypted in place on the first read. */
    private fun secret(key: String): String? {
        val v = p.getString(key, null) ?: return null
        if (SecretBox.isSealed(v)) return SecretBox.open(v)
        putSecret(key, v)
        return v
    }

    private fun putSecret(key: String, v: String?) =
        p.edit().apply { if (v == null) remove(key) else putString(key, SecretBox.seal(v)) }.apply()

    /** The deSEC API token for creating the wrapper site's name (WrapperUi); sealed like the other secrets. */
    var desecToken: String?
        get() = secret("desec_token")
        set(v) = putSecret("desec_token", v?.ifEmpty { null })

    /** Every setting, secrets decrypted — for the password-protected backup file (Backup). */
    fun exportAll(): JSONObject {
        val out = JSONObject()
        for ((k, v) in p.all) out.put(k, when (v) {
            is String -> JSONObject().put("t", "s").put("v", if (k in SECRETS) secret(k) else v)
            is Boolean -> JSONObject().put("t", "b").put("v", v)
            is Int -> JSONObject().put("t", "i").put("v", v)
            is Long -> JSONObject().put("t", "l").put("v", v)
            is Float -> JSONObject().put("t", "f").put("v", v.toDouble())
            is Set<*> -> JSONObject().put("t", "ss").put("v", org.json.JSONArray(v.toList()))
            else -> continue
        })
        return JSONObject().put("v", 1).put("app", "yggtunnel").put("created", System.currentTimeMillis()).put("prefs", out)
    }

    /** Replaces every setting with [backup] (from [exportAll]); secrets are sealed with this phone's Keystore key. */
    fun importAll(backup: JSONObject) {
        require(backup.optString("app") == "yggtunnel" && backup.optInt("v") == 1) { "not a YggTunnel backup" }
        val all = backup.getJSONObject("prefs")
        val e = p.edit().clear()
        for (k in all.keys()) {
            val o = all.getJSONObject(k)
            when (o.getString("t")) {
                "s" -> if (k in SECRETS) e.putString(k, SecretBox.seal(o.getString("v"))) else e.putString(k, o.getString("v"))
                "b" -> e.putBoolean(k, o.getBoolean("v"))
                "i" -> e.putInt(k, o.getInt("v"))
                "l" -> e.putLong(k, o.getLong("v"))
                "f" -> e.putFloat(k, o.getDouble("v").toFloat())
                "ss" -> e.putStringSet(k, o.getJSONArray("v").let { a -> (0 until a.length()).map { a.getString(it) }.toSet() })
            }
        }
        e.apply()
    }

    /** Generated once, so the Yggdrasil address stays the same between runs. */
    val config: String
        get() = secret("config") ?: Native.generateConfig().also {
            if (!Native.isError(it)) putSecret("config", it)
        }

    var peers: String
        get() = p.getString("peers", null) ?: DEFAULT_PEERS.joinToString("\n")
        set(v) = p.edit().putString("peers", v.trim()).apply()

    /** The phone's WireGuard key pair, generated once; only the public key goes to the server. */
    val wgKeys: JSONObject
        get() = secret("wg_keys")?.let { JSONObject(it) } ?: JSONObject(Native.wgKeyPair()).also {
            putSecret("wg_keys", it.toString())
        }

    /**
     * All servers, each a profile: id, host, port, user, key, passphrase, hostKey, result (from server.sh), wssPeer —
     * or an imported one (imported, clientKey, directPeer). One of them is active ([server]).
     */
    var servers: List<JSONObject>
        get() {
            migrateServer()
            return secret("servers")?.let { s -> org.json.JSONArray(s).let { a -> (0 until a.length()).map { a.getJSONObject(it) } } } ?: emptyList()
        }
        set(v) = putSecret("servers", if (v.isEmpty()) null else org.json.JSONArray(v).toString())

    private var activeServerId: String?
        get() = p.getString("active_server", null)
        set(v) = p.edit().putString("active_server", v).apply()

    /** 0.2–0.11 kept a single profile under "server": it becomes the first entry of [servers]. */
    private fun migrateServer() {
        if (!p.contains("server")) return
        val old = secret("server")?.let { JSONObject(it) }
        p.edit().remove("server").apply()
        if (old != null && !p.contains("servers")) {
            old.put("id", newId())
            putSecret("servers", org.json.JSONArray(listOf(old)).toString())
            activeServerId = old.getString("id")
        }
    }

    private fun newId() = java.util.UUID.randomUUID().toString().take(8)

    /** The active server profile, or null. Setting it updates the active entry (null removes it). */
    var server: JSONObject?
        get() = servers.let { list -> list.firstOrNull { it.optString("id") == activeServerId } ?: list.firstOrNull() }
        set(v) {
            val list = servers.toMutableList()
            val id = server?.optString("id")
            if (v == null) {
                val old = server
                list.removeAll { it.optString("id") == id }
                servers = list
                swapServerPeers(old, list.firstOrNull())
                activeServerId = list.firstOrNull()?.optString("id")
                return
            }
            if (!v.has("id")) v.put("id", id ?: newId())
            val i = list.indexOfFirst { it.optString("id") == v.optString("id") }
            if (i >= 0) list[i] = v else list += v
            servers = list
            activeServerId = v.getString("id")
        }

    /** Replaces the saved server with the same id as [v]; the active server stays as it was. */
    fun updateServer(v: JSONObject) {
        servers = servers.map { if (it.optString("id") == v.optString("id")) v else it }
    }

    /** Adds [v] as a new server and makes it active (its own peers are put first in the list). */
    fun addServer(v: JSONObject) {
        val old = server
        v.put("id", newId())
        servers = servers + v
        swapServerPeers(old, v)
        activeServerId = v.getString("id")
    }

    /** Makes the server [id] active: the old server's own peers leave the list, the new one's come first. */
    fun switchServer(id: String) {
        val old = server
        val new = servers.firstOrNull { it.optString("id") == id } ?: return
        swapServerPeers(old, new)
        activeServerId = id
    }

    private fun swapServerPeers(old: JSONObject?, new: JSONObject?) {
        val gone = serverPeers(old, serverLink)
        val own = if (ownPeers) serverPeers(new, serverLink) else emptyList()
        peers = (own + peerList.without(gone).without(own)).joinToString("\n")
    }

    /** Our server's own peers (tls, quic, wss) are always used, first, whatever the stored list holds. */
    var ownPeers: Boolean
        get() = p.getBoolean("own_peers", true)
        set(v) = p.edit().putBoolean("own_peers", v).apply()

    /** The peers actually used and shown: with [ownPeers] the server's own peers first, then the stored list. */
    val effectivePeers: List<String>
        get() = if (!ownPeers) peerList else serverPeers(server, serverLink).let { own -> own + peerList.without(own) }

    /** Peer test: transports to test, working ones only, how many at most (0 — all), how many best to take. */
    var testSchemes: Set<String>
        get() = p.getStringSet("test_schemes", setOf("tls", "quic", "wss"))!!.toSet()
        set(v) = p.edit().putStringSet("test_schemes", v).apply()
    var testWorkingOnly: Boolean
        get() = p.getBoolean("test_working_only", true)
        set(v) = p.edit().putBoolean("test_working_only", v).apply()
    /** Peer test: the speed is measured for this many best by ping, 0 — for all (every peer is pinged). */
    var speedCount: Int
        get() = p.getInt("speed_count", 20)
        set(v) = p.edit().putInt("speed_count", v).apply()
    var bestCount: Int
        get() = p.getInt("best_count", 6)
        set(v) = p.edit().putInt("best_count", v).apply()

    /** Connection log (ConnLog) while connected, and how often it checks, s. */
    var connLog: Boolean
        get() = p.getBoolean("conn_log", true)
        set(v) = p.edit().putBoolean("conn_log", v).apply()
    /** The VPN was on and has not been turned off since: still set at the next start = that session ended
     *  without «VPN off» (the app was updated, the system closed the process, a crash). Written at once. */
    var sessionOpen: Boolean
        get() = p.getBoolean("session_open", false)
        set(v) { p.edit().putBoolean("session_open", v).commit() }
    var connLogInterval: Int
        get() = p.getInt("conn_log_interval", 10)
        set(v) = p.edit().putInt("conn_log_interval", v).apply()

    /** Public peers only as a fallback: parked while a direct link to our server is up. */
    var serverOnlyPeers: Boolean
        get() = p.getBoolean("server_only_peers", true)
        set(v) = p.edit().putBoolean("server_only_peers", v).apply()

    /** The preferred link to our server: tls (default), quic or wss — the others stay as fallbacks. */
    var serverLink: String
        get() = p.getString("server_link", "tls")!!.takeIf { it in LINKS } ?: "tls"
        set(v) = p.edit().putString("server_link", v).apply()

    /** Links to the server for the tunnel (go/lanes.go): 1 — the node's own only; more — extra nodes, each with its own TLS link. */
    var lanes: Int
        get() = p.getInt("server_lanes", 1).takeIf { it in LANES } ?: 1
        set(v) = p.edit().putInt("server_lanes", v).apply()

    /** Connect when the app is opened. */
    var autoConnect: Boolean
        get() = p.getBoolean("auto_connect", false)
        set(v) = p.edit().putBoolean("auto_connect", v).apply()

    /** All traffic through the server (when one is set up); off — only the Yggdrasil network. */
    var fullTunnel: Boolean
        get() = p.getBoolean("full_tunnel", true)
        set(v) = p.edit().putBoolean("full_tunnel", v).apply()

    /** The set-up server's result (go/server.sh), when the full tunnel should be used. */
    val tunnelServer: JSONObject?
        get() = if (fullTunnel) server?.optJSONObject("result") else null

    /** The peer list is the automatic one (catalog / defaults), not edited by hand — it may be refreshed. */
    var peersAuto: Boolean
        get() = p.getBoolean("peers_auto", true)
        set(v) = p.edit().putBoolean("peers_auto", v).apply()

    /** When the list last came from the catalog, ms. */
    val peersUpdated: Long get() = p.getLong("peers_updated", 0)

    /** A catalog list: the server's own peer (if set up) stays first. */
    fun setCatalogPeers(list: List<String>) {
        val own = if (ownPeers) serverPeers(server, serverLink) else emptyList()
        peers = (own + list.without(own)).joinToString("\n")
        p.edit().putBoolean("peers_auto", true).putLong("peers_updated", System.currentTimeMillis()).apply()
    }

    val peerList: List<String> get() = peers.split(WHITESPACE).filter { it.isNotBlank() }

    var theme: Theme
        get() = runCatching { Theme.valueOf(p.getString("theme", null) ?: "") }.getOrDefault(Theme.SYSTEM)
        set(v) = p.edit().putString("theme", v.name).apply()

    /** Translucent cards and dialogs (as in AppShelf). */
    var translucent: Boolean
        get() = p.getBoolean("translucent", true)
        set(v) = p.edit().putBoolean("translucent", v).apply()

    /** A status notification with Disconnect while connected (off by default since 0.10.2: the tile does it). */
    var statusNotification: Boolean
        get() = p.getBoolean("status_notification", false)
        set(v) = p.edit().putBoolean("status_notification", v).apply()

    /** Do not ask about background work (battery optimisation) again. */
    var batteryAsked: Boolean
        get() = p.getBoolean("battery_asked", false)
        set(v) = p.edit().putBoolean("battery_asked", v).apply()

    /** Reconnect by itself after a change the running VPN would only pick up on reconnect. */
    var autoReconnect: Boolean
        get() = p.getBoolean("auto_reconnect", true)
        set(v) = p.edit().putBoolean("auto_reconnect", v).apply()

    /** Auto-pick: how many of the fastest peers stay connected (0 — off, all listed peers). 0.4–0.9 had an on/off switch. */
    var keepPeers: Int
        get() = p.getInt("keep_peers", if (p.getBoolean("auto_peers", true)) KEEP_PEERS else 0)
        set(v) = p.edit().putInt("keep_peers", v).apply()

    /** Per-app routing: false — the listed apps bypass the VPN (blacklist), true — only the listed apps use it (whitelist). */
    var onlyListedApps: Boolean
        get() = p.getBoolean("only_listed_apps", false)
        set(v) = p.edit().putBoolean("only_listed_apps", v).apply()

    /** Packages that bypass the VPN (blacklist mode). */
    var excludedApps: Set<String>
        get() = p.getStringSet("excluded_apps", emptySet())!!.toSet()
        set(v) = p.edit().putStringSet("excluded_apps", v).apply()

    /** Packages that use the VPN (whitelist mode). */
    var includedApps: Set<String>
        get() = p.getStringSet("included_apps", emptySet())!!.toSet()
        set(v) = p.edit().putStringSet("included_apps", v).apply()

    /** Devices added by 0.5 (kept on the phone then): public key → private key; DevicesUi moves them to the server. */
    var localDevices: Map<String, String>
        get() = secret("local_devices")?.let { s -> JSONObject(s).let { o -> o.keys().asSequence().associateWith { o.getString(it) } } } ?: emptyMap()
        set(v) = putSecret("local_devices", JSONObject(v).toString())

    /** Puts [uri] first in the peer list (the server itself — the shortest path). */
    fun addPeerFirst(uri: String) {
        peers = (listOf(uri) + peerList.without(listOf(uri))).joinToString("\n")
    }

    companion object {
        /** Settings encrypted with the Keystore key (SecretBox). */
        val SECRETS = setOf("config", "wg_keys", "server", "servers", "local_devices", "desec_token")

        /** How many peers auto-pick keeps by default (besides the server's own). */
        const val KEEP_PEERS = 3
        val KEEP_CHOICES = listOf(0, 2, 3, 4, 5, 6)

        /** The server itself as a Yggdrasil peer — the shortest path. */
        fun directPeer(host: String, port: Int) = "tls://${if (':' in host) "[$host]" else host}:$port"

        /**
         * The server's own peers, pinned (never parked by auto-pick) and kept first: direct TLS, QUIC on the same
         * port (server.sh listens on both) and wss through its site.
         */
        fun serverPeers(server: JSONObject?, preferred: String = "tls"): List<String> {
            if (server == null) return emptyList()
            val direct = directPeerOf(server)
            // Yggdrasil sends over the link with the lowest priority among those to the same node, and both ends
            // honour it. TLS by default: QUIC (UDP) was the obvious choice, but on the user's mobile network it gave
            // 1.75 Mbit/s down against 48 over TLS (03.10) — UDP slower there, or quic-go starved of socket buffers
            val links = listOfNotNull(direct, direct?.takeIf { it.startsWith("tls://") }?.replaceFirst("tls://", "quic://"),
                server.optString("wssPeer").ifEmpty { null })
            val order = links.sortedBy { if (it.substringBefore("://") == preferred) 0 else 1 } // stable: tls, quic, wss
            return links.map { l -> order.indexOf(l).let { i -> if (i == 0) withoutPriority(l) else withPriority(l, i) } }
        }

        private fun withoutPriority(uri: String): String {
            val rest = uri.substringAfter('?', "").split('&').filter { it.isNotEmpty() && !it.startsWith("priority=") }
            return base(uri) + if (rest.isEmpty()) "" else "?" + rest.joinToString("&")
        }

        /** [uri] with ?priority=[p] (replacing one it may have). */
        fun withPriority(uri: String, p: Int): String {
            val base = uri.substringBefore('?')
            val rest = uri.substringAfter('?', "").split('&').filter { it.isNotEmpty() && !it.startsWith("priority=") }
            return base + "?" + (rest + "priority=$p").joinToString("&")
        }

        /** A peer URI without its options (?key=…, ?priority=…): the same link whatever the options. */
        fun base(uri: String) = uri.substringBefore('?')

        /** This list without the peers in [other], options ignored. */
        fun List<String>.without(other: List<String>): List<String> {
            val drop = other.map(::base).toSet()
            return filter { base(it) !in drop }
        }

        fun directPeerOf(server: JSONObject): String? =
            server.optString("directPeer").ifEmpty { null }
                ?: server.optJSONObject("result")?.takeIf { it.has("yggPort") }?.let { directPeer(server.optString("host"), it.optInt("yggPort")) }

        val LINKS = listOf("tls", "quic", "wss")
        val LANES = listOf(1, 2, 3, 4)

        const val WG_PORT = 51820
        const val YGG_PORT = 21443

        /** Nearby public peers (github.com/yggdrasil-network/public-peers), TLS/QUIC/WSS only. */
        val DEFAULT_PEERS = listOf(
            "tls://ygg-msk-1.averyan.ru:8362",
            "quic://ygg-msk-1.averyan.ru:8364",
            "tls://yggno.de:18227",
            "tls://45.147.200.202:443",
            "quic://ru2.cert.dev:7042",
            "wss://ygg.mvault.ru.net:443",
        )
    }
}
