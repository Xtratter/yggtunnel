package io.github.xtratter.yggtunnel

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PrivacyTest {
    private fun covered(text: String, vararg extra: String) = Privacy.ranges(text, *extra).map { text.substring(it) }

    @Test fun peerUriHidesEverythingAfterScheme() =
        assertEquals(listOf("powershell.example.org:443/abc"), covered("wss://powershell.example.org:443/abc"))

    @Test fun userAtHostPort() = assertEquals(listOf("vpn.example.org:2222"), covered("root@vpn.example.org:2222"))

    @Test fun yggdrasilAddress() =
        assertEquals(listOf("200:bf43:143e:341c:1b38:13a6:4118:28e4"), covered("200:bf43:143e:341c:1b38:13a6:4118:28e4"))

    @Test fun ipv4InText() = assertEquals(listOf("10.66.66.2"), covered("WireGuard: port 51820 · your IP 10.66.66.2 · peers"))

    @Test fun versionsAndTimesStay() = assertTrue(covered("yggdrasil 0.5.14 at 14:23:05, 51820").isEmpty())

    @Test fun deviceNameAsExtra() = assertEquals(listOf("SDY_Smart"), covered("● SDY_Smart", "SDY_Smart"))

    @Test fun noOverlaps() {
        val text = "read tcp 192.168.1.126:47094->45.147.200.202:443: reset"
        val r = Privacy.ranges(text)
        for (i in r.indices) for (j in i + 1 until r.size) assertTrue(r[i].last < r[j].first)
        assertEquals(listOf("192.168.1.126:47094", "45.147.200.202:443"), r.map { text.substring(it) })
    }
}
