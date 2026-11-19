// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.Build
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
}
