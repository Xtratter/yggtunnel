package io.github.xtratter.yggtunnel

import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URI
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.json.JSONObject

/**
 * Fresh public peers from publicpeers.neilalexander.dev (the yggdrasil-network/public-peers list
 * with hourly up/down checks): reliable ones (up ≥ 90% of the recent checks), timed from this
 * phone by TCP connect; the fastest TLS/WSS ones plus QUIC on the same hosts. Auto-pick then keeps
 * the best few connected. Runs on the caller's thread — call it off the main thread.
 */
object PeerCatalog {
    private const val URL = "https://publicpeers.neilalexander.dev/publicnodes.json"
    private const val TAKE = 6

    fun fetch(server: JSONObject?): List<String> {
        val data = download(server)
        val all = mutableListOf<String>()
        for (country in data.keys()) {
            val peers = data.getJSONObject(country)
            for (uri in peers.keys()) {
                val p = peers.getJSONObject(uri)
                if (p.optBoolean("up") && reliability(p) >= 0.9) all += uri
            }
        }
        val timed = all.filter { it.startsWith("tls://") || it.startsWith("wss://") }
        val pool = Executors.newFixedThreadPool(32)
        val rtt = timed.associateWith { uri -> pool.submit<Long?> { connectMs(uri) } }
        val fastest = rtt.mapNotNull { (uri, f) -> runCatching { f.get(5, TimeUnit.SECONDS) }.getOrNull()?.let { uri to it } }
            .sortedBy { it.second }.map { it.first }.take(TAKE)
        pool.shutdownNow()
        // QUIC to the same hosts: another transport, often faster and not TCP-in-TCP
        val hosts = fastest.mapNotNull { host(it) }.toSet()
        val quic = all.filter { it.startsWith("quic://") && host(it) in hosts }.take(2)
        return fastest + quic
    }

    /** One peer of the public list: [reliability] — share of the recent hourly checks it was up (0…1). */
    data class Entry(val uri: String, val country: String, val up: Boolean, val reliability: Double) {
        val scheme get() = uri.substringBefore("://")
    }

    /** The whole public list, as is (no timing). */
    fun fetchAll(server: JSONObject?): List<Entry> {
        val data = download(server)
        val all = mutableListOf<Entry>()
        for (country in data.keys()) {
            val peers = data.getJSONObject(country)
            for (uri in peers.keys()) {
                val p = peers.getJSONObject(uri)
                all += Entry(uri, country.removeSuffix(".md").replace('-', ' '), p.optBoolean("up"), reliability(p))
            }
        }
        return all.sortedWith(compareBy({ it.country }, { it.uri }))
    }

    /** Share of the recent checks a peer was up: from the site's "states" ("*" — up), or "r" from the server. */
    private fun reliability(p: JSONObject): Double {
        if (p.has("r")) return p.optDouble("r")
        val st = p.optString("states")
        return if (st.isEmpty()) 0.0 else st.count { it == '*' }.toDouble() / st.length
    }

    /** Whether the last list came through the server (the site is not reachable from this phone directly). */
    @Volatile var viaServer = false; private set

    /**
     * The list straight from the site; if that fails (this app is outside the VPN, and the site may be
     * unreachable directly), [server] fetches it and hands it over SSH (go/store.sh ACTION=catalog).
     */
    private fun download(server: JSONObject?): JSONObject {
        val direct = runCatching { direct() }
        direct.getOrNull()?.let { viaServer = false; return it }
        if (!ServerCall.canSsh(server)) throw direct.exceptionOrNull()!!
        val (r, err) = ServerCall.runBlocking(server!!, "store", JSONObject().put("ACTION", "catalog"))
        r ?: throw java.io.IOException("${direct.exceptionOrNull()?.message}; through the server: $err")
        viaServer = true
        return r
    }

    private fun direct(): JSONObject {
        val c = java.net.URL(URL).openConnection() as HttpURLConnection
        c.connectTimeout = 8000; c.readTimeout = 15000
        c.setRequestProperty("User-Agent", "YggTunnel") // the site refuses some default agents (403)
        return JSONObject(c.inputStream.bufferedReader().use { it.readText() })
    }

    private fun host(uri: String) = runCatching { URI(uri).host }.getOrNull()

    private fun connectMs(uri: String): Long? = runCatching {
        val u = URI(uri)
        val port = if (u.port > 0) u.port else 443
        val t = System.nanoTime()
        Socket().use { it.connect(InetSocketAddress(u.host, port), 2000) }
        (System.nanoTime() - t) / 1_000_000
    }.getOrNull()
}
