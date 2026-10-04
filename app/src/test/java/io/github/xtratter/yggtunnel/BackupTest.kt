package io.github.xtratter.yggtunnel

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class BackupTest {
    private val json = """{"v":1,"prefs":{"server":{"t":"s","v":"{\"key\":\"-----BEGIN OPENSSH PRIVATE KEY-----\"}"}}}"""

    @Test fun roundTrip() = assertEquals(json, Backup.open(Backup.seal(json, "correct horse".toCharArray()), "correct horse".toCharArray()))

    @Test(expected = Backup.BadPassword::class)
    fun wrongPassword() { Backup.open(Backup.seal(json, "correct horse".toCharArray()), "wrong horse!".toCharArray()) }

    @Test(expected = Backup.BadPassword::class)
    fun damagedFile() {
        val b = Backup.seal(json, "correct horse".toCharArray())
        b[b.size - 5] = (b[b.size - 5].toInt() xor 1).toByte()
        Backup.open(b, "correct horse".toCharArray())
    }

    @Test(expected = Backup.NotABackup::class)
    fun otherFile() { Backup.open("hello, this is not a backup at all".toByteArray(), "x".toCharArray()) }

    @Test fun noPlainTextInside() {
        val b = String(Backup.seal(json, "correct horse".toCharArray()), Charsets.ISO_8859_1)
        assertFalse(b.contains("OPENSSH"))
    }

    @Test fun saltMakesEveryFileDifferent() {
        val p = "correct horse".toCharArray()
        assertFalse(Backup.seal(json, p).contentEquals(Backup.seal(json, p)))
    }
}
