package io.github.xtratter.yggtunnel

import org.junit.Assert.assertEquals
import org.junit.Test
import java.util.Calendar

class ConnLogViewTest {
    private val lines = listOf(
        "03.10 22:59:08  ● VPN выключен",
        "04.10 12:35:10  ● VPN включён (через сервер)",
        "04.10 12:35:21  8.8.8.8 — · сервер —",
        "04.10 12:35:31  8.8.8.8 115 · сервер 115",
        "04.10 12:36:00  ‼ сервер не отвечает",
        "04.10 12:36:20  ✓ сервер снова отвечает, перерыв 20 с",
    )
    private fun now(): Calendar = Calendar.getInstance().apply { set(2026, 9, 4, 14, 0, 0) }

    @Test fun newestFirstByDayTodayUnnamed() {
        val rows = ConnLog.view(lines, events = true, max = 100, today = "04.10")
        assertEquals(listOf(ConnLog.Kind.DAY, ConnLog.Kind.BACK, ConnLog.Kind.LOST, ConnLog.Kind.INFO, ConnLog.Kind.DAY, ConnLog.Kind.INFO), rows.map { it.kind })
        assertEquals("", rows[0].text) // today: shown as «Today»
        assertEquals("12:36:20", rows[1].time)
        assertEquals("03.10", rows[4].text)
    }

    @Test fun everythingIncludesChecksAndMaxLimits() {
        assertEquals(2, ConnLog.view(lines, events = false, max = 100, today = "04.10").count { it.kind == ConnLog.Kind.CHECK })
        assertEquals(2, ConnLog.view(lines, events = false, max = 2, today = "04.10").count { it.kind != ConnLog.Kind.DAY })
    }

    @Test fun lastDayCountsOnlyTheLast24h() {
        assertEquals(2 to 1, ConnLog.lastDay(lines, now()))
        val later = now().apply { add(Calendar.DAY_OF_MONTH, 2) }
        assertEquals(0 to 0, ConnLog.lastDay(lines, later))
    }

    @Test fun timeAcrossNewYear() {
        val jan = Calendar.getInstance().apply { set(2027, 0, 1, 0, 10, 0) }
        val t = ConnLog.timeOf("31.12 23:59:00  x", jan)!!
        assertEquals(2026, Calendar.getInstance().apply { timeInMillis = t }.get(Calendar.YEAR))
    }
}
