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
import java.security.spec.ECGenParameterSpec
import java.util.concurrent.FutureTask
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import javax.crypto.KeyAgreement
import org.junit.After
import org.junit.FixMethodOrder
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.MethodSorters

/**
 * A probe, not a test: it asserts nothing and logs under "synctang-probe".
 *
 * Two real unlock attempts on a Pixel 3a XL (Android 12) each recognised the
 * fingerprint and then spent exactly six seconds in the Keystore returning
 * BACKEND_BUSY (from upgrade_keyblob_if_required) before reporting the
 * authentication as expired. This asks whether that depends on the key being
 * gated by a biometric, by the strong-box request, or by the biometric
 * invalidation flag, by timing the first use of each kind of key with NO
 * authentication. The stall comes before the Keystore's authentication check,
 * so it shows without a finger: a healthy gated key fails at once with
 * "User not authenticated".
 */
@RunWith(AndroidJUnit4::class)
@FixMethodOrder(MethodSorters.NAME_ASCENDING)
class GatedKeyUseProbeTest {

    private val tag = "synctang-probe"
    private val aliases = mutableListOf<String>()

    @After
    fun cleanUp() {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        aliases.forEach { runCatching { ks.deleteEntry(it) } }
    }

    private fun chain(e: Throwable): String =
        generateSequence(e) { it.cause }.joinToString(" <- ") { "${it.javaClass.simpleName}: ${it.message}" }

    private fun spec(alias: String, gated: Boolean, validity: Int, invalidate: Boolean, strongBox: Boolean) =
        KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setUserAuthenticationRequired(gated)
            .apply {
                if (gated) {
                    setUserAuthenticationParameters(validity, KeyProperties.AUTH_BIOMETRIC_STRONG)
                    setInvalidatedByBiometricEnrollment(invalidate)
                }
            }
            .setIsStrongBoxBacked(strongBox)
            .build()

    /** Times [block] on its own thread, giving up after 20 s, and returns "ok|exception in N ms". */
    private fun timed(block: () -> Unit): String {
        val task = FutureTask { block() }
        val start = System.nanoTime()
        Thread(task, "probe-use").apply { isDaemon = true }.start()
        val outcome = try {
            task.get(20, TimeUnit.SECONDS)
            "ok"
        } catch (e: TimeoutException) {
            "STILL STUCK after 20 s"
        } catch (e: java.util.concurrent.ExecutionException) {
            chain(e.cause ?: e)
        }
        return "$outcome in ${TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start)} ms"
    }

    private fun probe(label: String, alias: String, gated: Boolean, validity: Int, invalidate: Boolean, strongBox: Boolean) {
        aliases += alias
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        try {
            val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
            gen.initialize(spec(alias, gated, validity, invalidate, strongBox))
            gen.generateKeyPair()
        } catch (e: Exception) {
            Log.i(tag, "$label: key creation failed: ${chain(e)}")
            return
        }
        val priv = ks.getKey(alias, null) as java.security.PrivateKey
        val level = KeyFactory.getInstance(priv.algorithm, "AndroidKeyStore")
            .getKeySpec(priv, KeyInfo::class.java).securityLevel
        Log.i(tag, "$label: created, securityLevel=$level")
        // Two uses in a row: is the first slow and the second fast (a one-off
        // upgrade), or are both slow?
        for (n in 1..2) {
            val result = timed {
                val ka = KeyAgreement.getInstance("ECDH")
                ka.init(priv)
                // Reached only if init was allowed: finish the agreement against
                // our own public point.
                ka.doPhase(ks.getCertificate(alias).publicKey, true)
                ka.generateSecret()
            }
            Log.i(tag, "$label: use $n: $result")
        }
    }

    @Test
    fun a1_ungatedControl() = probe("ungated, no strongbox", "gp-ungated", gated = false, validity = 0, invalidate = false, strongBox = false)

    @Test
    fun b1_gatedLikeTheApp() = probe("gated 5 s, invalidate, strongbox requested (the app's key)", "gp-app", gated = true, validity = 5, invalidate = true, strongBox = true)

    @Test
    fun b2_gatedNoStrongBox() = probe("gated 5 s, invalidate, no strongbox", "gp-nosb", gated = true, validity = 5, invalidate = true, strongBox = false)

    @Test
    fun b3_gatedNoInvalidate() = probe("gated 5 s, NO invalidate, no strongbox", "gp-noinv", gated = true, validity = 5, invalidate = false, strongBox = false)

    @Test
    fun b4_gated15s() = probe("gated 15 s, invalidate, no strongbox", "gp-15", gated = true, validity = 15, invalidate = true, strongBox = false)

    @Test
    fun c1_ungatedAgain() = probe("ungated again, strongbox requested", "gp-ungated-sb", gated = false, validity = 0, invalidate = false, strongBox = true)
}
