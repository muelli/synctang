// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.hardware.biometrics.BiometricManager
import android.os.Build
import android.os.CancellationSignal
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import java.security.interfaces.ECPublicKey
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Log
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.spec.ECGenParameterSpec
import javax.crypto.KeyAgreement
import org.junit.Assert.assertNotNull
import org.junit.Test

/**
 * A probe, not a test of the app: what this device's Keystore and BiometricPrompt offer for a key agreement key that
 * needs a fingerprint for every use bound to the operation, which is `BiometricPrompt.CryptoObject(KeyAgreement)`,
 * said to be new in API 36.1 (Android 16 QPR2). Each step logs under the tag KAProbe what it found, and the
 * steps that need what an older Android lacks stop with the reason.
 */
class KeyAgreementProbeTest {
    private val tag = "KAProbe"
    private val alias = "synctang-probe-agreement"

    private fun cryptoObjectClass(): Class<*> = Class.forName("android.hardware.biometrics.BiometricPrompt\$CryptoObject")

    @Test
    fun theDeviceSaysWhatItIs() {
        val full = runCatching { Build::class.java.getField("SDK_INT_FULL").getInt(null) }.getOrNull()
        Log.i(tag, "SDK_INT=${Build.VERSION.SDK_INT} SDK_INT_FULL=$full release=${Build.VERSION.RELEASE} codename=${Build.VERSION.CODENAME} model=${Build.MODEL}")
    }

    @Test
    fun theFrameworkCryptoObjectHasAConstructorForAKeyAgreement() {
        val ctor = runCatching { cryptoObjectClass().getConstructor(KeyAgreement::class.java) }
        Log.i(tag, "CryptoObject(KeyAgreement) constructor: " + ctor.fold({ "present: $it" }, { "absent: $it" }))
        assertNotNull("no CryptoObject(KeyAgreement) constructor", ctor.getOrNull())
    }

    @Test
    fun anAgreementKeyThatNeedsAFingerprintCanBeMadeAndInitialised() {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        ks.deleteEntry(alias)
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        gen.initialize(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setUserAuthenticationRequired(true)
                .setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                .build(),
        )
        val pair = gen.generateKeyPair()
        Log.i(tag, "key made: ${pair.public.algorithm} ${pair.public.format}")
        val agreement = KeyAgreement.getInstance("ECDH", "AndroidKeyStore")
        agreement.init(ks.getKey(alias, null) as java.security.PrivateKey)
        Log.i(tag, "KeyAgreement initialised with the auth-bound key: provider=${agreement.provider.name}")
        ks.deleteEntry(alias)
    }

    @Test
    fun theCryptoObjectCanBeBuiltFromThatKeyAgreementAndGivesItBack() {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        ks.deleteEntry(alias)
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        gen.initialize(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setUserAuthenticationRequired(true)
                .setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                .build(),
        )
        gen.generateKeyPair()
        val agreement = KeyAgreement.getInstance("ECDH", "AndroidKeyStore")
        agreement.init(ks.getKey(alias, null) as java.security.PrivateKey)
        val built = cryptoObjectClass().getConstructor(KeyAgreement::class.java).newInstance(agreement)
        val back = built.javaClass.getMethod("getKeyAgreement").invoke(built)
        Log.i(tag, "CryptoObject built: $built, getKeyAgreement() returns the same: ${back === agreement}")
        ks.deleteEntry(alias)
        assertNotNull(back)
    }

    /**
     * The whole path, as the app would walk it: a key agreement key that needs a fingerprint for each use, the framework
     * prompt given a CryptoObject made from its KeyAgreement, a touch of the virtual sensor, and then the agreement
     * itself, whose secret has to be what a software ECDH of the same two keys gives. Needs a fingerprint enrolled (the
     * workflow does it, and touches the sensor while this waits); without one it says so and fails.
     */
    @Test
    fun theWholeHardwarePathGivesTheSecretASoftwareAgreementGives() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val can = context.getSystemService(BiometricManager::class.java).canAuthenticate(android.hardware.biometrics.BiometricManager.Authenticators.BIOMETRIC_STRONG)
        Log.i(tag, "canAuthenticate(BIOMETRIC_STRONG)=$can (0 is success)")
        assertEquals("a strong biometric is enrolled", BiometricManager.BIOMETRIC_SUCCESS, can)

        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        ks.deleteEntry(alias)
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        gen.initialize(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setUserAuthenticationRequired(true)
                .setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                .build(),
        )
        val pair = gen.generateKeyPair()
        val peer = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()
        val expected = KeyAgreement.getInstance("ECDH").apply { init(peer.private); doPhase(pair.public as ECPublicKey, true) }.generateSecret()

        val agreement = KeyAgreement.getInstance("ECDH", "AndroidKeyStore")
        agreement.init(ks.getKey(alias, null) as java.security.PrivateKey)

        // Before the fingerprint: what the Keystore does with an agreement that has not been authenticated.
        val before = runCatching { KeyAgreement.getInstance("ECDH", "AndroidKeyStore").apply { init(ks.getKey(alias, null) as java.security.PrivateKey); doPhase(peer.public, true) }.generateSecret() }
        Log.i(tag, "an unauthenticated agreement: " + before.fold({ "GAVE A SECRET (bad)" }, { "refused: ${it.javaClass.simpleName}: ${it.message}" }))

        val crypto = cryptoObjectClass().getConstructor(KeyAgreement::class.java).newInstance(agreement) as android.hardware.biometrics.BiometricPrompt.CryptoObject
        val done = CountDownLatch(1)
        var outcome = "none"
        val executor = Executors.newSingleThreadExecutor()
        val callback = object : android.hardware.biometrics.BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: android.hardware.biometrics.BiometricPrompt.AuthenticationResult) {
                outcome = "succeeded, crypto object returned: ${result.cryptoObject != null}"
                done.countDown()
            }
            override fun onAuthenticationError(errorCode: Int, errString: CharSequence?) {
                outcome = "error $errorCode $errString"
                done.countDown()
            }
        }
        val prompt = android.hardware.biometrics.BiometricPrompt.Builder(context)
            .setTitle("probe")
            .setNegativeButton("cancel", executor) { _, _ -> outcome = "cancelled"; done.countDown() }
            .setAllowedAuthenticators(android.hardware.biometrics.BiometricManager.Authenticators.BIOMETRIC_STRONG)
            .build()
        // The prompt belongs to an app that is in the foreground: the system cancels it otherwise.
        ActivityScenario.launch(MainActivity::class.java).use {
            Thread.sleep(2_000)
            prompt.authenticate(crypto, CancellationSignal(), executor, callback)
            assertTrue("the prompt did not finish within a minute (is the sensor being touched?)", done.await(60, TimeUnit.SECONDS))
        }
        Log.i(tag, "the prompt: $outcome")

        val got = agreement.apply { doPhase(peer.public, true) }.generateSecret()
        Log.i(tag, "secret from the Keystore: ${got.size} bytes, equal to the software ECDH: ${got.contentEquals(expected)}")
        assertArrayEquals("the Keystore's secret is not the software ECDH's", expected, got)

        // One authentication, one use.
        val again = runCatching { agreement.apply { doPhase(peer.public, true) }.generateSecret() }
        Log.i(tag, "a second use of the same agreement: " + again.fold({ "WORKED (the authentication was not for one use)" }, { "refused: ${it.javaClass.simpleName}: ${it.message}" }))
        ks.deleteEntry(alias)
    }
}
