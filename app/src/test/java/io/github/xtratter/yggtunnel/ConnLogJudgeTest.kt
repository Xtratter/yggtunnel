package io.github.xtratter.yggtunnel

import org.junit.Assert.assertEquals
import org.junit.Test

class ConnLogJudgeTest {
    private val ok = 80.0

    @Test fun oneMissIsNotAnOutage() {
        val j = ConnLog.Judge(10_000)
        assertEquals(emptyList<Any>(), j.check(0, listOf("s" to ok)))
        assertEquals(emptyList<Any>(), j.check(10_000, listOf("s" to null))) // a lost probe under load
        assertEquals(emptyList<Any>(), j.check(20_000, listOf("s" to ok)))
    }

    @Test fun twoMissesAreAnOutageDatedFromTheFirst() {
        val j = ConnLog.Judge(10_000)
        j.check(0, listOf("s" to ok))
        assertEquals(emptyList<Any>(), j.check(10_000, listOf("s" to null)))
        assertEquals(listOf(ConnLog.Judge.Lost("s")), j.check(20_000, listOf("s" to null)))
        assertEquals(emptyList<Any>(), j.check(30_000, listOf("s" to null)))
        assertEquals(listOf(ConnLog.Judge.Back("s", 30)), j.check(40_000, listOf("s" to ok)))
    }

    @Test fun targetsAreJudgedApart() {
        val j = ConnLog.Judge(10_000)
        j.check(0, listOf("a" to null, "b" to ok))
        assertEquals(listOf(ConnLog.Judge.Lost("a")), j.check(10_000, listOf("a" to null, "b" to null)))
    }

    @Test fun aLateCheckMeansThePhoneSlept() {
        val j = ConnLog.Judge(10_000)
        j.check(0, listOf("s" to ok))
        assertEquals(emptyList<Any>(), j.check(25_000, listOf("s" to ok))) // a bit late: fine
        assertEquals(listOf(ConnLog.Judge.Paused(500)), j.check(525_000, listOf("s" to ok)))
    }

    @Test fun skippedChecksAreNotAPause() {
        val j = ConnLog.Judge(10_000)
        j.check(0, listOf("s" to ok))
        for (t in 10_000L..300_000L step 10_000) j.skip(t) // our own speed test
        assertEquals(emptyList<Any>(), j.check(310_000, listOf("s" to ok)))
    }

    @Test fun aChangedIntervalIsNotAPause() {
        val j = ConnLog.Judge(10_000)
        j.check(0, listOf("s" to ok), 10_000)
        for (t in 30_000L..300_000L step 30_000) // switched to 30 s
            assertEquals(emptyList<Any>(), j.check(t, listOf("s" to ok), 30_000))
        assertEquals(listOf(ConnLog.Judge.Paused(400)), j.check(700_000, listOf("s" to ok), 30_000))
    }
}
