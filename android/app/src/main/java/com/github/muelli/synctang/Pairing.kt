// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.security.SecureRandom

/**
 * One paired machine: which machine to dial, and what to call it in the
 * UI before an unlock is approved.
 *
 * The name is a label the machine chose for itself and is not
 * authenticated by anything. [deviceId] is, so the two are always shown
 * together.
 */
data class Machine(val deviceId: String, val name: String)

/**
 * What the app persists between runs.
 *
 * Deliberately not here: the MR-1 private scalar, which never leaves
 * the Keystore. What is here is the machine's identity and this
 * phone's own transport identity seed. Neither unlocks anything on its
 * own, but rewriting the machine's Device ID would redirect an unlock
 * at an attacker's machine, so this uses EncryptedSharedPreferences
 * rather than a plain file.
 */
class PairingStore(context: Context) {

    private val prefs: SharedPreferences by lazy {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context,
            "synctang-pairing",
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    /**
     * This phone's transport identity seed, generated on first use. The
     * Device ID the machine authorises is derived from it, so losing it
     * means re-pairing, and it must not change behind the user's back.
     */
    fun identitySeed(): String {
        prefs.getString(KEY_SEED, null)?.let { return it }
        val fresh = ByteArray(32).also { SecureRandom().nextBytes(it) }.toHex()
        prefs.edit().putString(KEY_SEED, fresh).apply()
        return fresh
    }

    fun machine(): Machine? {
        val id = prefs.getString(KEY_MACHINE_ID, null) ?: return null
        return Machine(id, prefs.getString(KEY_MACHINE_NAME, null).orEmpty())
    }

    fun saveMachine(machine: Machine) {
        prefs.edit()
            .putString(KEY_MACHINE_ID, machine.deviceId)
            .putString(KEY_MACHINE_NAME, machine.name)
            .apply()
    }

    fun forgetMachine() {
        prefs.edit().remove(KEY_MACHINE_ID).remove(KEY_MACHINE_NAME).apply()
    }

    private companion object {
        const val KEY_SEED = "identity-seed"
        const val KEY_MACHINE_ID = "machine-device-id"
        const val KEY_MACHINE_NAME = "machine-name"
    }
}

/**
 * The pairing payload the machine shows as a QR code.
 *
 * Two forms are accepted, because a QR code is a convenience and not a
 * security boundary: whatever is scanned is only a Device ID to dial,
 * and the dial itself verifies that identity cryptographically.
 *
 *  - `synctang://pair?machine=<device-id>&name=<label>`
 *  - a bare Syncthing Device ID, with or without its dashes
 */
object PairingPayload {

    fun parse(scanned: String): Machine? {
        val text = scanned.trim()
        if (text.isEmpty()) return null

        if (text.startsWith(SCHEME, ignoreCase = true)) {
            val query = text.substringAfter('?', "")
            val fields = query.split('&').mapNotNull { field ->
                val name = field.substringBefore('=', "")
                val value = field.substringAfter('=', "")
                if (name.isEmpty()) null else name to urlDecode(value)
            }.toMap()
            val id = normaliseDeviceId(fields["machine"].orEmpty()) ?: return null
            return Machine(id, fields["name"].orEmpty())
        }

        val id = normaliseDeviceId(text) ?: return null
        return Machine(id, "")
    }

    /**
     * Accepts a Device ID in any of the shapes a human might paste it
     * in, and returns the canonical dashed upper-case form, or null if
     * it is not a Device ID at all.
     */
    fun normaliseDeviceId(raw: String): String? {
        val compact = raw.uppercase().filter { it in BASE32_ALPHABET }
        if (compact.length != COMPACT_LENGTH) return null
        return compact.chunked(GROUP).joinToString("-")
    }

    private fun urlDecode(value: String): String =
        try {
            java.net.URLDecoder.decode(value, "UTF-8")
        } catch (e: IllegalArgumentException) {
            value
        }

    private const val SCHEME = "synctang://pair"
    private const val BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
    private const val GROUP = 7
    private const val COMPACT_LENGTH = 56
}

internal fun ByteArray.toHex(): String =
    joinToString("") { "%02x".format(it) }
