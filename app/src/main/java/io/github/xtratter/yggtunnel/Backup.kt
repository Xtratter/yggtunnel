package io.github.xtratter.yggtunnel

import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec

/**
 * A password-protected settings file (for a new phone or a reinstall — Android backups are off):
 * "YGTB1" | 16-byte salt | 12-byte IV | AES-256-GCM(JSON). The key comes from the password via
 * PBKDF2-HMAC-SHA256 with [ITERATIONS]; GCM also detects a wrong password or a damaged file.
 * Plain JVM crypto — unit-tested.
 */
object Backup {
    private val MAGIC = "YGTB1".toByteArray()
    const val ITERATIONS = 310_000
    const val MIN_PASSWORD = 8

    class BadPassword : Exception()
    class NotABackup : Exception()

    private fun key(password: CharArray, salt: ByteArray) = SecretKeySpec(
        SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256")
            .generateSecret(PBEKeySpec(password, salt, ITERATIONS, 256)).encoded, "AES")

    fun seal(json: String, password: CharArray): ByteArray {
        val rnd = SecureRandom()
        val salt = ByteArray(16).also(rnd::nextBytes)
        val iv = ByteArray(12).also(rnd::nextBytes)
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key(password, salt), GCMParameterSpec(128, iv)) }
        c.updateAAD(MAGIC)
        return MAGIC + salt + iv + c.doFinal(json.toByteArray())
    }

    /** The JSON inside; [NotABackup] for another file, [BadPassword] for a wrong password or a damaged file. */
    fun open(data: ByteArray, password: CharArray): String {
        if (data.size < MAGIC.size + 28 + 16 || !data.copyOfRange(0, MAGIC.size).contentEquals(MAGIC)) throw NotABackup()
        val salt = data.copyOfRange(5, 21)
        val iv = data.copyOfRange(21, 33)
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE, key(password, salt), GCMParameterSpec(128, iv)) }
        c.updateAAD(MAGIC)
        return try { String(c.doFinal(data, 33, data.size - 33)) } catch (e: javax.crypto.AEADBadTagException) { throw BadPassword() }
    }
}
