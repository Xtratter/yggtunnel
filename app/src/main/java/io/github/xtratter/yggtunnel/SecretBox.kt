package io.github.xtratter.yggtunnel

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Encrypts secrets kept in preferences (the node key, WireGuard keys, the SSH key of the server) with
 * an AES-256-GCM key that lives in the Android Keystore and never leaves it — a copy of the app's files
 * alone is useless. Stored form: "enc1:" + base64(12-byte IV + ciphertext).
 */
object SecretBox {
    private const val ALIAS = "yggtunnel-prefs"
    private const val PREFIX = "enc1:"

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build())
        }.generateKey()
    }

    fun isSealed(s: String) = s.startsWith(PREFIX)

    fun seal(plain: String): String {
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        return PREFIX + Base64.getEncoder().encodeToString(c.iv + c.doFinal(plain.toByteArray()))
    }

    /** The plain text, or null when it cannot be opened (the Keystore key is gone, e.g. after a restore elsewhere). */
    fun open(sealed: String): String? = runCatching {
        val b = Base64.getDecoder().decode(sealed.removePrefix(PREFIX))
        val c = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, b, 0, 12)) }
        String(c.doFinal(b, 12, b.size - 12))
    }.getOrNull()
}
