package io.github.xtratter.yggtunnel

import io.github.xtratter.yggtunnel.StatusIslandModel.Event
import io.github.xtratter.yggtunnel.StatusIslandModel.Kind
import io.github.xtratter.yggtunnel.StatusIslandModel.Phase
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class StatusIslandModelTest {
    @Test fun startingIsConnecting() {
        assertEquals(Event(Kind.CONNECTING), StatusIslandModel.onPhase(Phase.OFF, Phase.STARTING, null))
        assertEquals(Event(Kind.CONNECTING), StatusIslandModel.onPhase(Phase.ON, Phase.STARTING, null)) // a reconnect
    }

    @Test fun onAloneIsNotAnEvent() {
        assertNull(StatusIslandModel.onPhase(Phase.STARTING, Phase.ON, null))
    }

    @Test fun offAfterRunningIsDisconnected() {
        assertEquals(Event(Kind.DISCONNECTED), StatusIslandModel.onPhase(Phase.ON, Phase.OFF, null))
        assertEquals(Event(Kind.DISCONNECTED), StatusIslandModel.onPhase(Phase.STARTING, Phase.OFF, null))
    }

    @Test fun offWithErrorIsFailed() {
        assertEquals(Event(Kind.FAILED, "no VPN permission"), StatusIslandModel.onPhase(Phase.STARTING, Phase.OFF, "no VPN permission"))
    }

    @Test fun offFromOffIsNothing() {
        assertNull(StatusIslandModel.onPhase(Phase.OFF, Phase.OFF, null))
        assertNull(StatusIslandModel.onPhase(null, Phase.OFF, null))
    }

    @Test fun connectedNeedsOk() {
        assertNull(StatusIslandModel.connected(ok = false, peers = 2, viaServer = false))
        assertEquals(Event(Kind.CONNECTED, "2"), StatusIslandModel.connected(ok = true, peers = 2, viaServer = false))
        assertEquals(Event(Kind.CONNECTED, "server"), StatusIslandModel.connected(ok = true, peers = 2, viaServer = true))
    }

    @Test fun dedupeDropsRepeatsOfTheSameKind() {
        val d = StatusIslandModel.Dedupe()
        assertEquals(Event(Kind.CONNECTING), d.accept(Event(Kind.CONNECTING)))
        assertNull(d.accept(Event(Kind.CONNECTING)))
        assertEquals(Event(Kind.CONNECTED, "2"), d.accept(Event(Kind.CONNECTED, "2")))
        assertNull(d.accept(Event(Kind.CONNECTED, "3")))
        assertEquals(Event(Kind.CONNECTING), d.accept(Event(Kind.CONNECTING))) // a reconnect after a connection
        assertNull(d.accept(null))
    }

    @Test fun failedIsAlwaysShown() {
        val d = StatusIslandModel.Dedupe()
        assertEquals(Event(Kind.FAILED, "a"), d.accept(Event(Kind.FAILED, "a")))
        assertEquals(Event(Kind.FAILED, "b"), d.accept(Event(Kind.FAILED, "b")))
    }
}
