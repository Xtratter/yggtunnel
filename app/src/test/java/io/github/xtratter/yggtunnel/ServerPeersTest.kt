package io.github.xtratter.yggtunnel

import io.github.xtratter.yggtunnel.Prefs.Companion.without
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class ServerPeersTest {
    private val s = JSONObject().put("host", "203.0.113.5").put("result", JSONObject().put("yggPort", 21443))
        .put("wssPeer", "wss://example.org:443/path")

    @Test fun tlsByDefault() {
        assertEquals(listOf("tls://203.0.113.5:21443", "quic://203.0.113.5:21443?priority=1", "wss://example.org:443/path?priority=2"),
            Prefs.serverPeers(s))
    }

    @Test fun preferredLinkFirst() {
        assertEquals(listOf("tls://203.0.113.5:21443?priority=1", "quic://203.0.113.5:21443", "wss://example.org:443/path?priority=2"),
            Prefs.serverPeers(s, "quic"))
        assertEquals(listOf("tls://203.0.113.5:21443?priority=1", "quic://203.0.113.5:21443?priority=2", "wss://example.org:443/path"),
            Prefs.serverPeers(s, "wss"))
    }

    @Test fun ipv6AndImported() {
        val v6 = JSONObject().put("host", "2001:db8::1").put("result", JSONObject().put("yggPort", 21443))
        assertEquals(listOf("tls://[2001:db8::1]:21443", "quic://[2001:db8::1]:21443?priority=1"), Prefs.serverPeers(v6))
        val imported = JSONObject().put("directPeer", "tls://h:1?key=ab")
        assertEquals(listOf("tls://h:1?key=ab", "quic://h:1?key=ab&priority=1"), Prefs.serverPeers(imported))
        assertEquals(emptyList<String>(), Prefs.serverPeers(JSONObject().put("host", "h")))
    }

    @Test fun oldEntriesAreTheSameLinks() {
        // lists saved by older versions hold the server's peers with other options: not added twice
        val own = Prefs.serverPeers(s)
        assertEquals(listOf("tls://other:2"),
            listOf("tls://203.0.113.5:21443?priority=1", "wss://example.org:443/path", "quic://203.0.113.5:21443", "tls://other:2").without(own))
    }
}
