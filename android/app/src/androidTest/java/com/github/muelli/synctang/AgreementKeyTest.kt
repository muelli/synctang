// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Context
import androidx.biometric.BiometricManager
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import java.security.KeyPairGenerator
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import javax.crypto.KeyAgreement
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * The phone's key as a key agreement key in the secure hardware ([AgreementKey]), through the real gate
 * ([BiometricGateImpl]): after a fingerprint the key gives the 32 bytes a software ECDH of the same two keys gives, and
 * before one it gives nothing. Needs Android 16 QPR2 (API 36.1) and an enrolled strong biometric, which the workflow
 * `emulator` makes on its 36.1 emulator (a virtual fingerprint, touched while this waits); anywhere else it is skipped, with
 * the reason.
 */
class AgreementKeyTest {
    private val context: Context = InstrumentationRegistry.getInstrumentation().targetContext

    // On the workflow's 36.1 emulator the test must not skip: a fingerprint that was not enrolled would then pass as
    // "skipped", and say nothing. It says so there (requireHardware), and skips everywhere else.
    private val required = InstrumentationRegistry.getArguments().getString("requireHardware") == "true"

    private fun need(ok: Boolean, why: String) = if (required) assertTrue("needed here: $why", ok) else assumeTrue(why, ok)

    @Test
    fun theKeyAnswersAfterAFingerprintWithTheSecretASoftwareAgreementGivesAndRefusesBefore() {
        need(HardwareAgreement.supported(), "BiometricPrompt.CryptoObject(KeyAgreement), new in Android 16 QPR2")
        need(
            BiometricManager.from(context).canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS,
            "an enrolled strong biometric",
        )

        val key = AgreementKey(alias = "synctang-agreement-test")
        key.delete()
        try {
            key.ensureKey()
            assertEquals(65, key.publicPoint().size)
            val peer = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()
            val challenge = sec1Of(peer.public as ECPublicKey)
            val expected = KeyAgreement.getInstance("ECDH").apply {
                init(peer.private)
                doPhase(ecPublicKeyFromSec1(key.publicPoint()), true)
            }.generateSecret()

            // Before a fingerprint: nothing.
            val refused = runCatching { key.agree(key.prepareAgreement(), challenge) }
            assertTrue("an agreement that was not authenticated gave a secret", refused.isFailure)

            // After one, through the real gate, from an activity in the foreground (the system cancels the prompt of an app that
            // is not).
            val agreement = key.prepareAgreement()
            val outcome = ActivityScenario.launch(MainActivity::class.java).use { scenario ->
                var gate: BiometricGateImpl? = null
                scenario.onActivity { gate = BiometricGateImpl(it) }
                Thread.sleep(2_000)
                runBlocking { gate!!.authenticate("test", "test", agreement) }
            }
            assertEquals(AuthOutcome.SUCCEEDED, outcome)
            val got = key.agree(agreement, challenge)
            assertEquals(32, got.size)
            assertArrayEquals("the key's secret is not the software ECDH's", expected, got)
        } finally {
            key.delete()
        }
    }
}
