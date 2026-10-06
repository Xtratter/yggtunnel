package io.github.xtratter.yggtunnel

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ServerRouteTest {
    @Test fun directIsNotNoted() {
        assertNull(ServerRoute.of("Connecting to root@192.0.2.1:22\nServer key: SHA256:x\nok"))
    }

    @Test fun connectedThroughYggdrasil() {
        val log = "Connecting to root@192.0.2.1:22\nConnecting through Yggdrasil\nThrough Yggdrasil: connected\nServer key: x"
        assertEquals(ServerRoute.Connected, ServerRoute.of(log))
    }

    @Test fun vpnOff() {
        assertEquals(ServerRoute.VpnOff, ServerRoute.of("Connecting to a\nThrough Yggdrasil: the VPN is off"))
    }

    @Test fun failedKeepsTheReason() {
        assertEquals(ServerRoute.Failed("i/o timeout"), ServerRoute.of("Through Yggdrasil: failed: i/o timeout\n"))
    }

    @Test fun lastLineWins() {
        assertEquals(ServerRoute.Connected, ServerRoute.of("Through Yggdrasil: failed: x\nThrough Yggdrasil: connected"))
    }
}
