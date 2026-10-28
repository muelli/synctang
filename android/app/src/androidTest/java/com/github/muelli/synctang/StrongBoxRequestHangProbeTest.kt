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
import org.junit.Test
import org.junit.runner.RunWith

/**
 * A probe, not a test: it asserts nothing and logs under "synctang-probe".
 *
 * GatedKeyUseProbeTest saw one ungated key-agreement key hang in the Keystore
 * (BACKEND_BUSY from upgrade_keyblob_if_required, for as long as anyone
 * waited), and it was the one created with StrongBox requested; the control
 * created without the request worked. This repeats both, alternating, with
 * the full key agreement, to see whether the request is what decides it.
 */
@RunWith(AndroidJUnit4::class)
class StrongBoxRequestHangProbeTest {

    private val tag = "synctang-probe"
    private val aliases = mutableListOf<String>()

    @After
    fun cleanUp() {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        aliases.forEach { runCatching { ks.deleteEntry(it) } }
    }

    private fun chain(e: Throwable): String =
        generateSequence(e) { it.cause }.joinToString(" <- ") { "${it.javaClass.simpleName}: ${it.message}" }

    private fun oneRound(n: Int, strongBoxRequested: Boolean) {
        val label = "round $n, strongbox ${if (strongBoxRequested) "REQUESTED" else "not requested"}"
        val alias = "sbh-$n-${if (strongBoxRequested) "sb" else "tee"}"
        aliases += alias
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        try {
            KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore").apply {
                initialize(
                    KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
                        .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                        .setIsStrongBoxBacked(strongBoxRequested)
                        .build(),
                )
            }.generateKeyPair()
        } catch (e: Exception) {
            Log.i(tag, "$label: key creation failed: ${chain(e)}")
            return
        }
        val priv = ks.getKey(alias, null) as java.security.PrivateKey
        val level = KeyFactory.getInstance(priv.algorithm, "AndroidKeyStore")
            .getKeySpec(priv, KeyInfo::class.java).securityLevel
        val task = FutureTask {
            val ka = KeyAgreement.getInstance("ECDH")
            ka.init(priv)
            ka.doPhase(ks.getCertificate(alias).publicKey, true)
            ka.generateSecret().size
        }
        val start = System.nanoTime()
        Thread(task, "probe-use").apply { isDaemon = true }.start()
        val outcome = try {
            "ok (${task.get(10, TimeUnit.SECONDS)} bytes)"
        } catch (e: TimeoutException) {
            "STUCK for 10 s"
        } catch (e: java.util.concurrent.ExecutionException) {
            chain(e.cause ?: e)
        }
        Log.i(tag, "$label: securityLevel=$level, agreement $outcome in ${TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start)} ms")
    }

    @Test
    fun alternating() {
        // not, requested, not, requested, requested, not: also shows whether a
        // stuck key leaves the Keystore stuck for the next, ordinary one.
        listOf(false, true, false, true, true, false).forEachIndexed { i, sb -> oneRound(i + 1, sb) }
    }
}
