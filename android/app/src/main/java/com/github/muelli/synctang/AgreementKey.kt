// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.ProviderException
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import javax.crypto.KeyAgreement

/**
 * The phone's key holder key as a key agreement key in the secure hardware ([HardwareAgreement]): it never leaves it,
 * and the app never holds a scalar, wrapped or not. Every use needs a strong biometric, bound to that operation through
 * `BiometricPrompt.CryptoObject(KeyAgreement)`, and the Keystore hands back only the x-coordinate of the shared point,
 * which is what MR-1 asks of a key holder. The public point the machines are enrolled against is the key's own.
 *
 * To answer a request: [prepareAgreement] gives a KeyAgreement, the biometric prompt authenticates it, [agree] finishes it
 * with the machine's challenge point and returns the 32 bytes the Go side sends ([Session.AnswerWithSharedX]).
 */
class AgreementKey(
    private val alias: String = DEFAULT_ALIAS,
    /** Always true in the app; the tests that cannot present a fingerprint set it to false. */
    private val requireUserAuthentication: Boolean = true,
) {
    private val keyStore: KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    fun exists(): Boolean = keyStore.containsAlias(alias)

    /**
     * Makes the key if there is none, StrongBox first and the TEE if StrongBox will not do, each tried once before anything
     * depends on it (see [KeyHolderKey]: a Titan M made keys it could not use), and reports where it ended up.
     */
    fun ensureKey(): KeyHolderKey.Backing {
        if (!exists()) create()
        return backing()
    }

    private fun create() {
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        var made = false
        try {
            generator.initialize(spec(strongBox = true))
            generator.generateKeyPair()
            made = canBeUsed("StrongBox")
        } catch (e: ProviderException) {
            // No secure element, or one that errors instead of saying so; only the StrongBox attempt is read this way.
            android.util.Log.w(LOG_TAG, "StrongBox cannot hold the agreement key", e)
        } catch (e: java.security.GeneralSecurityException) {
            if (requireUserAuthentication && e.looksLikeMissingBiometricEnrolment()) {
                delete()
                throw BiometricEnrolmentRequired(e)
            }
            android.util.Log.w(LOG_TAG, "StrongBox cannot hold the agreement key", e)
        } catch (e: IllegalStateException) {
            delete()
            throw BiometricEnrolmentRequired(e)
        }
        if (!made) {
            if (exists()) keyStore.deleteEntry(alias)
            try {
                generator.initialize(spec(strongBox = false))
                generator.generateKeyPair()
            } catch (e: java.security.GeneralSecurityException) {
                delete()
                if (requireUserAuthentication && e.looksLikeMissingBiometricEnrolment()) throw BiometricEnrolmentRequired(e)
                throw e
            } catch (e: IllegalStateException) {
                delete()
                throw BiometricEnrolmentRequired(e)
            }
            if (!canBeUsed("the TEE")) {
                delete()
                throw SecureHardwareUnresponsive()
            }
        }
        keyStore.load(null)
    }

    /** Starts one agreement with the key just made, bounded, and says whether the Keystore answered. */
    private fun canBeUsed(where: String): Boolean =
        try {
            runWithin(TRIAL_SECONDS) { prepareAgreement() }
            true
        } catch (e: KeyUseTimedOut) {
            android.util.Log.w(LOG_TAG, "an agreement key made in $where did not answer within ${TRIAL_SECONDS}s", e)
            false
        } catch (e: Exception) {
            android.util.Log.w(LOG_TAG, "an agreement key made in $where cannot be used", e)
            false
        }

    private fun spec(strongBox: Boolean): KeyGenParameterSpec =
        KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setUserAuthenticationRequired(requireUserAuthentication)
            .apply {
                if (requireUserAuthentication) {
                    // Zero seconds: authentication per use, for this operation only, through a CryptoObject.
                    setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                    // A new fingerprint or face must not silently extend who can unlock the machine.
                    setInvalidatedByBiometricEnrollment(true)
                }
            }
            .setIsStrongBoxBacked(strongBox)
            .build()

    /** Where Android says the key was made: see [KeyHolderKey.Backing]. */
    fun backing(): KeyHolderKey.Backing {
        val private = privateKey()
        val level = KeyFactory.getInstance(private.algorithm, ANDROID_KEYSTORE)
            .getKeySpec(private, android.security.keystore.KeyInfo::class.java)
            .securityLevel
        return KeyHolderKey.classify(level)
    }

    /** The public point S, SEC1 uncompressed: what `unlocker enrol --pubkey <hex>` and a pairing carry. */
    fun publicPoint(): ByteArray = sec1Of(keyStore.getCertificate(alias).publicKey as ECPublicKey)

    /** kid = sha256(S), how a machine names which enrolled key holder a request is for. */
    fun kid(): ByteArray = MessageDigest.getInstance("SHA-256").digest(publicPoint())

    /**
     * A KeyAgreement ready for the machine's point, which goes to the biometric prompt as its CryptoObject. Throws
     * [KeyInvalidated] when the key has been retired for good, which the Keystore does the moment a fingerprint or face
     * is added, and reports before any prompt.
     */
    fun prepareAgreement(): KeyAgreement =
        try {
            KeyAgreement.getInstance("ECDH", ANDROID_KEYSTORE).apply { init(privateKey()) }
        } catch (e: KeyPermanentlyInvalidatedException) {
            throw KeyInvalidated(e)
        }

    /**
     * Finishes an agreement the biometric prompt has authenticated, with the machine's challenge [point]: the 32-byte
     * x-coordinate of the shared point. The caller hands it to the Go side and wipes it. Throws [KeyRefusedFingerprint]
     * when the hardware refuses the fingerprint it was just given.
     */
    fun agree(agreement: KeyAgreement, point: ByteArray): ByteArray =
        try {
            agreement.doPhase(ecPublicKeyFromSec1(point), true)
            agreement.generateSecret()
        } catch (e: Exception) {
            if (e.isUserNotAuthenticated()) throw KeyRefusedFingerprint(e)
            throw e
        }

    /** Deletes the key. Every machine enrolled against it becomes unrecoverable through this phone. */
    fun delete() {
        if (keyStore.containsAlias(alias)) keyStore.deleteEntry(alias)
    }

    private fun privateKey(): PrivateKey =
        keyStore.getKey(alias, null) as? PrivateKey
            ?: throw IllegalStateException("no agreement key under alias $alias")

    companion object {
        const val DEFAULT_ALIAS = "synctang-key-holder-agreement"
        private const val TRIAL_SECONDS = 8L
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val LOG_TAG = "AgreementKey"
    }
}
