// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import javax.crypto.KeyAgreement
import org.junit.After
import org.junit.FixMethodOrder
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.MethodSorters

/**
 * A probe, not a test: it asserts nothing, and logs what a phone does when
 * asked for the kind of key this app uses, under the tag "synctang-probe".
 *
 * It exists because the app asks for a StrongBox key and silently takes
 * whatever it gets, and a run on a real phone that advertises StrongBox
 * reported the key as living in the ordinary TEE without saying why, and
 * then hung in the Keystore on use. The attestation certificate is the
 * authoritative statement of where a key was made, independent of what the
 * KeyInfo API reports.
 *
 * Ordered by name, and the key-use step last, because that is the step that
 * hung and a hang ends the run.
 */
@RunWith(AndroidJUnit4::class)
@FixMethodOrder(MethodSorters.NAME_ASCENDING)
class StrongBoxProbeTest {

    private val tag = "synctang-probe"
    private val aliases = mutableListOf<String>()

    @After
    fun cleanUp() {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        aliases.forEach { runCatching { ks.deleteEntry(it) } }
    }

    private fun spec(alias: String, strongBox: Boolean, auth: Boolean): KeyGenParameterSpec =
        KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setUserAuthenticationRequired(auth)
            .apply {
                if (auth) {
                    setUserAuthenticationParameters(5, KeyProperties.AUTH_BIOMETRIC_STRONG)
                    setInvalidatedByBiometricEnrollment(true)
                }
            }
            .setAttestationChallenge(byteArrayOf(1, 2, 3, 4))
            .setIsStrongBoxBacked(strongBox)
            .build()

    private fun chain(e: Throwable): String =
        generateSequence(e) { it.cause }.joinToString(" <- ") { "${it.javaClass.name}: ${it.message}" }

    private fun levels(cert: X509Certificate): String {
        val ext = cert.getExtensionValue("1.3.6.1.4.1.11129.2.1.17") ?: return "no attestation extension"
        return try {
            var p = 0
            fun len(): Int {
                var l = ext[p++].toInt() and 0xff
                if (l < 0x80) return l
                val n = l and 0x7f
                l = 0
                repeat(n) { l = (l shl 8) or (ext[p++].toInt() and 0xff) }
                return l
            }
            fun int(): Int {
                val l = len()
                var v = 0
                for (i in 0 until l) v = (v shl 8) or (ext[p + i].toInt() and 0xff)
                p += l
                return v
            }
            require(ext[p++] == 0x04.toByte()); len()
            require(ext[p++] == 0x30.toByte()); len()
            require(ext[p++] == 0x02.toByte()); val version = int()
            require(ext[p++] == 0x0A.toByte()); val attLevel = int()
            require(ext[p++] == 0x02.toByte()); val kmVersion = int()
            require(ext[p++] == 0x0A.toByte()); val kmLevel = int()
            "attestationVersion=$version attestationSecurityLevel=$attLevel keymasterVersion=$kmVersion " +
                "keymasterSecurityLevel=$kmLevel (0=software, 1=TEE, 2=StrongBox)"
        } catch (e: Exception) {
            "could not parse the attestation extension: $e"
        }
    }

    private fun describe(label: String, alias: String, strongBox: Boolean, auth: Boolean) {
        aliases += alias
        val started = System.nanoTime()
        try {
            val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
            gen.initialize(spec(alias, strongBox, auth))
            gen.generateKeyPair()
        } catch (e: Throwable) {
            Log.i(tag, "$label: generation FAILED after ${(System.nanoTime() - started) / 1_000_000} ms: ${chain(e)}")
            return
        }
        val millis = (System.nanoTime() - started) / 1_000_000
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val private = ks.getKey(alias, null) as PrivateKey
        val info = KeyFactory.getInstance(private.algorithm, "AndroidKeyStore").getKeySpec(private, KeyInfo::class.java)
        Log.i(
            tag,
            "$label: generated in $millis ms; KeyInfo.securityLevel=${info.securityLevel} (1=TEE, 2=StrongBox) " +
                "insideSecureHardware=${info.isInsideSecureHardware}",
        )
        val leaf = ks.getCertificateChain(alias)?.firstOrNull() as? X509Certificate
        Log.i(tag, "$label: attestation: ${if (leaf == null) "no certificate" else levels(leaf)}")
    }

    @Test
    fun a1_strongBoxRequestedNoAuth() = describe("strongbox, no auth", "probe-sb-noauth", true, false)

    @Test
    fun a2_strongBoxRequestedWithAuth() = describe("strongbox, 5 s biometric auth (the app's real key)", "probe-sb-auth", true, true)

    @Test
    fun a3_teeControl() = describe("tee control, no auth", "probe-tee-noauth", false, false)

    // A control for the whole probe: an ordinary signing key, which every
    // Keymaster generation supports in hardware. If these land in the TEE and
    // StrongBox while the key-agreement keys above land in software, the
    // phone's hardware is fine and the fallback is specific to key agreement.
    private fun describeSigning(label: String, alias: String, strongBox: Boolean) {
        aliases += alias
        val started = System.nanoTime()
        try {
            val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
            gen.initialize(
                KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                    .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                    .setDigests(KeyProperties.DIGEST_SHA256)
                    .setAttestationChallenge(byteArrayOf(1, 2, 3, 4))
                    .setIsStrongBoxBacked(strongBox)
                    .build(),
            )
            gen.generateKeyPair()
        } catch (e: Throwable) {
            Log.i(tag, "$label: generation FAILED after ${(System.nanoTime() - started) / 1_000_000} ms: ${chain(e)}")
            return
        }
        val millis = (System.nanoTime() - started) / 1_000_000
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val private = ks.getKey(alias, null) as PrivateKey
        val info = KeyFactory.getInstance(private.algorithm, "AndroidKeyStore").getKeySpec(private, KeyInfo::class.java)
        val leaf = ks.getCertificateChain(alias)?.firstOrNull() as? X509Certificate
        Log.i(
            tag,
            "$label: generated in $millis ms; KeyInfo.securityLevel=${info.securityLevel} " +
                "insideSecureHardware=${info.isInsideSecureHardware}; attestation: ${if (leaf == null) "none" else levels(leaf)}",
        )
    }

    @Test
    fun a4_signingKeyTee() = describeSigning("SIGNING key, TEE", "probe-sign-tee", false)

    @Test
    fun a5_signingKeyStrongBox() = describeSigning("SIGNING key, StrongBox requested", "probe-sign-sb", true)

    /** Last, and bounded: the step that hung. The key agreement runs on its own thread. */
    @Test
    fun z9_useTheStrongBoxKey() {
        val alias = "probe-use"
        aliases += alias
        try {
            val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
            gen.initialize(spec(alias, true, false))
            gen.generateKeyPair()
        } catch (e: Throwable) {
            Log.i(tag, "use: generation FAILED: ${chain(e)}")
            return
        }
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val private = ks.getKey(alias, null) as PrivateKey
        // Any valid P-256 public key will do as the peer; use the platform's own.
        val peerGen = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }
        val peer = peerGen.generateKeyPair().public
        var outcome = "still running after 20 s (this is the hang)"
        val worker = Thread {
            outcome = try {
                val ka = KeyAgreement.getInstance("ECDH", "AndroidKeyStore")
                ka.init(private)
                ka.doPhase(peer, true)
                "ok, ${ka.generateSecret().size} bytes"
            } catch (e: Throwable) {
                "FAILED: ${chain(e)}"
            }
        }
        val started = System.nanoTime()
        worker.start()
        worker.join(20_000)
        Log.i(tag, "use: key agreement with the StrongBox-requested key: $outcome (${(System.nanoTime() - started) / 1_000_000} ms)")
    }
}
