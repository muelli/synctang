// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.security.SecureRandom
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

/**
 * One paired machine: which machine to dial, and what to call it in the
 * UI before an unlock is approved.
 *
 * The name is a label the machine chose for itself and is not
 * authenticated by anything. [deviceId] is, so the two are always shown
 * together.
 *
 * [discovery] is the discovery server this machine's owner runs in
 * place of the public Syncthing one, as the machine's pairing link gave
 * it; empty means the public default. A phone has to look the machine
 * up where it announces, so it is kept with the machine. There is no
 * relay setting: only the machine joins a relay, and the phone learns
 * which from discovery.
 */
data class Machine(
    val deviceId: String,
    val name: String,
    val discovery: String = "",
)

/**
 * What the app persists between runs.
 *
 * Deliberately not here: the MR-1 private scalar, which is kept
 * wrapped in files of its own ([KeyHolderKey]), the wrapping key being
 * in the Keystore. What is here is the machine's identity and this
 * phone's own transport identity seed. Neither unlocks anything on its
 * own, but rewriting the machine's Device ID would redirect an unlock
 * at an attacker's machine, so this uses EncryptedSharedPreferences
 * rather than a plain file.
 */
class PairingStore(context: Context) : MachineStore {

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

    /**
     * The paired machines, the one used last first, kept as one JSON list in the same encrypted file as
     * the rest, so that a rewritten Device ID is as hard here as it was when there was one machine. Only
     * what [Machine] holds goes in: no status, nothing secret.
     */
    override fun machines(): List<Machine> {
        val json = prefs.getString(KEY_MACHINES, null) ?: return emptyList()
        return try {
            val array = JSONArray(json)
            (0 until array.length()).map { i ->
                val o = array.getJSONObject(i)
                Machine(o.getString("id"), o.optString("name"), o.optString("discovery"))
            }
        } catch (e: JSONException) {
            // A list that cannot be read is no list: the phone is paired again, which is what the file is
            // for. Not a crash at start.
            emptyList()
        }
    }

    private fun write(machines: List<Machine>) {
        val array = JSONArray()
        for (m in machines) {
            array.put(JSONObject().put("id", m.deviceId).put("name", m.name).put("discovery", m.discovery))
        }
        // commit(), not apply(): a list half written when the app dies would be no list.
        prefs.edit().putString(KEY_MACHINES, array.toString()).commit()
    }

    override fun saveMachine(machine: Machine) {
        write(listOf(machine) + machines().filter { it.deviceId != machine.deviceId })
    }

    override fun forget(deviceId: String) {
        write(machines().filter { it.deviceId != deviceId })
    }

    override fun markUsed(deviceId: String) {
        val all = machines()
        val used = all.firstOrNull { it.deviceId == deviceId } ?: return
        if (all.first() == used) return
        write(listOf(used) + all.filter { it.deviceId != deviceId })
    }

    /**
     * Drops this phone's transport identity and the paired machine, so
     * that the next [identitySeed] is a fresh one. Only for a phone
     * whose key has been invalidated, when the old identity is already
     * of no use: every machine enrolled it alongside the dead key.
     */
    fun startNewIdentity() {
        prefs.edit()
            .remove(KEY_SEED)
            .remove(KEY_MACHINES)
            .commit()
    }

    /**
     * Whether the owner has accepted, on this phone, a key that Android
     * made in software. Off until they say so, and kept across a new
     * identity: it is about the phone, not about one key.
     */
    fun softwareKeyAllowed(): Boolean = prefs.getBoolean(KEY_SOFTWARE_ALLOWED, false)

    fun allowSoftwareKey() {
        prefs.edit().putBoolean(KEY_SOFTWARE_ALLOWED, true).commit()
    }

    private companion object {
        const val KEY_SEED = "identity-seed"
        const val KEY_MACHINES = "machines"
        const val KEY_SOFTWARE_ALLOWED = "software-key-allowed"
    }
}

/**
 * The pairing payload the machine shows as a QR code.
 *
 * Two forms are accepted, because a QR code is a convenience and not a
 * security boundary: whatever is scanned is only a Device ID to dial,
 * and the dial itself verifies that identity cryptographically.
 *
 *  - `synctang://pair?machine=<device-id>&name=<label>`, which may also carry
 *    `psk` (the one-time code), and `discovery` (the URL of the machine
 *    owner's own discovery server, instead of the public one)
 *  - a bare Syncthing Device ID, with or without its dashes
 *
 * A parameter not listed here is not ignored: see [unknownParameters].
 */
object PairingPayload {

    fun parse(scanned: String): Machine? {
        val text = scanned.trim()
        if (text.isEmpty()) return null

        if (text.startsWith(SCHEME, ignoreCase = true)) {
            val fields = fields(text)
            val id = normaliseDeviceId(fields["machine"].orEmpty()) ?: return null
            return Machine(id, fields["name"].orEmpty(), fields["discovery"].orEmpty())
        }

        val id = normaliseDeviceId(text) ?: return null
        return Machine(id, "")
    }

    /**
     * The one-time pairing code from a scanned payload, or null if it
     * carries none.
     *
     * Its absence is the difference between the two ways of enrolling
     * a phone. With a code, the phone can finish enrolment itself by
     * proving it holds the code and sending its public key over the
     * network. Without one, all that was scanned is a machine to
     * remember, and its public key still has to reach the machine some
     * other way. Both forms stay valid, so a code shown by an older
     * machine keeps working.
     *
     * An empty psk counts as absent rather than as a code that cannot
     * work, since that is what a machine that did not set one produces.
     */
    fun pairingCode(scanned: String): String? {
        val text = scanned.trim()
        if (!text.startsWith(SCHEME, ignoreCase = true)) return null
        return fields(text)["psk"]?.takeIf { it.isNotEmpty() }
    }

    /**
     * The names of the parameters in a scanned link that this app does
     * not know, made safe to put on a screen (they come from a QR code
     * anyone can print): letters, digits, `_`, `.` and `-`, at most 24
     * of them. Empty when there are none, or for a bare Device ID.
     *
     * The caller refuses the pairing when this is not empty. Ignoring an
     * unknown parameter would pair the phone as if the machine had asked
     * for nothing, when what it asked for may be exactly the thing that
     * keeps the phone from finding it, or from keeping to the servers its
     * owner chose.
     */
    fun unknownParameters(scanned: String): List<String> {
        val text = scanned.trim()
        if (!text.startsWith(SCHEME, ignoreCase = true)) return emptyList()
        return fields(text).keys.filter { it !in KNOWN }.map { name ->
            name.filter { it.isLetterOrDigit() || it in "_.-" }.take(MAX_NAME).ifEmpty { "?" }
        }
    }

    /**
     * The names of the settings a scanned link gives more than once, made safe to show as in
     * [unknownParameters]. Not decided by which of them "wins": the command line tool refuses such a
     * link, and so does the app. Empty when there are none, or for a bare Device ID.
     */
    fun repeatedParameters(scanned: String): List<String> {
        val text = scanned.trim()
        if (!text.startsWith(SCHEME, ignoreCase = true)) return emptyList()
        return allFields(text).groupBy({ it.first }, { it.second }).filterValues { it.size > 1 }.keys.map { name ->
            name.filter { it.isLetterOrDigit() || it in "_.-" }.take(MAX_NAME).ifEmpty { "?" }
        }
    }

    /**
     * "discovery" if that setting in a scanned link cannot be a server
     * URL, or null if it is fine or absent. A light check: the Go side validates for real when the
     * value is used, but a link that can never work is better refused
     * where it is scanned than at the first unlock.
     */
    fun badSetting(scanned: String): String? {
        val text = scanned.trim()
        if (!text.startsWith(SCHEME, ignoreCase = true)) return null
        val fields = fields(text)
        fields["discovery"]?.takeIf { it.isNotEmpty() && !HTTP_URL.matches(it) }?.let { return "discovery" }
        return null
    }

    /**
     * Whether a scanned link's discovery server is plain `http` without
     * `?insecure`. Plain http is for a lookup that says it is meant (as in
     * Syncthing's own discovery URLs); the Go side refuses the rest when the
     * server is used, so the app refuses it here, where it is scanned.
     */
    fun plainHttpDiscoveryWithoutInsecure(scanned: String): Boolean {
        val text = scanned.trim()
        if (!text.startsWith(SCHEME, ignoreCase = true)) return false
        val discovery = fields(text)["discovery"].orEmpty()
        if (!discovery.startsWith("http://", ignoreCase = true)) return false
        val insecure = discovery.substringAfter('?', "").split('&')
            .firstOrNull { it.substringBefore('=') == "insecure" }
            ?: return true
        return insecure.substringAfter('=', "") == "false"
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

    private fun allFields(text: String): List<Pair<String, String>> =
        text.substringAfter('?', "").split('&').mapNotNull { field ->
            val name = field.substringBefore('=', "")
            val value = field.substringAfter('=', "")
            if (name.isEmpty()) null else name to urlDecode(value)
        }

    private fun fields(text: String): Map<String, String> = allFields(text).toMap()

    private fun urlDecode(value: String): String =
        try {
            java.net.URLDecoder.decode(value, "UTF-8")
        } catch (e: IllegalArgumentException) {
            value
        }

    private val KNOWN = setOf("machine", "name", "psk", "discovery")
    private const val MAX_NAME = 24
    private val HTTP_URL = Regex("https?://[^/\\s]+\\S*", RegexOption.IGNORE_CASE)
    private const val SCHEME = "synctang://pair"
    private const val BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
    private const val GROUP = 7
    private const val COMPACT_LENGTH = 56
}

internal fun ByteArray.toHex(): String =
    joinToString("") { "%02x".format(it) }

/**
 * The command to run on the machine, once, so that this phone can
 * unlock it. Built as a string rather than shown as two loose hex
 * values because that is the form it is actually used in, and kept
 * out of the Composable so that it can be tested: the flags have to
 * be exactly right, and getting one wrong produces a phone that is
 * refused at unlock time with nothing to point back at the command.
 *
 * --recipient-transport-id, not --transport-id. The latter sets the
 * machine's own transport identity; this is the identity of the party
 * allowed to unlock it. Likewise --name is the machine's own name, not
 * this phone's, so it gets a placeholder for the person to fill in.
 *
 * The passphrase is read into a shell variable and handed to enrol
 * through a process substitution, exactly as the README does, so it
 * never touches a disk. Never a path to a passphrase file.
 */
fun enrolCommand(publicKeyHex: String, deviceId: String): String =
    "read -rsp 'LUKS passphrase: ' PASS\n" +
        "synctang-unlocker enrol --device /dev/sdXN --name my-machine \\\n" +
        "  --pubkey $publicKeyHex \\\n" +
        "  --recipient-transport-id $deviceId \\\n" +
        "  --existing-passphrase-file <(printf %s \"\$PASS\")"
