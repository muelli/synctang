// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec
import javax.crypto.KeyAgreement

/**
 * The key holder's long-term MR-1 key, held in the Android Keystore.
 *
 * The private scalar s is generated inside the Keystore (StrongBox when
 * the phone has it) and never leaves it, not even into this process.
 * The only thing this class can do with it is the one ECDH operation
 * MR-1 asks for: given the machine's challenge point X, return the
 * x-coordinate of s.X. That is exactly what
 * java.security.KeyAgreement.generateSecret() yields for an EC key,
 * confirmed on a device in docs/wp0-keystore-ecdh.md; it is the raw
 * coordinate, not a KDF output.
 *
 * ### How the biometric gate actually works
 *
 * There is no BiometricPrompt.CryptoObject constructor taking a
 * KeyAgreement. Checked against android.jar for API 34: CryptoObject
 * accepts Signature, Cipher, Mac, IdentityCredential and
 * PresentationSession, and nothing else. So the per-operation
 * (auth-per-use) binding available to a Cipher key is simply not
 * reachable for a PURPOSE_AGREE_KEY key, and a key created with
 * setUserAuthenticationParameters(0, ...) would be permanently
 * unusable: every doPhase would throw UserNotAuthenticatedException
 * because no operation-bound authentication token can ever be produced
 * for it.
 *
 * What is used instead is the time-bound form:
 * setUserAuthenticationParameters(AUTH_VALIDITY_SECONDS,
 * AUTH_BIOMETRIC_STRONG). The gate is still enforced by Keymint in the
 * secure world, not by this app: without a hardware-signed
 * authentication token no older than AUTH_VALIDITY_SECONDS, produced by
 * a strong biometric match, the ECDH fails. Skipping the app's
 * BiometricPrompt call, or racing it, does not help an attacker,
 * because the Keystore, not the UI, is what refuses. The window is kept
 * short so that a successful authentication cannot be banked and reused
 * much later.
 *
 * AUTH_BIOMETRIC_STRONG deliberately excludes the device credential:
 * the threat this app is built for includes someone who has the phone
 * and its PIN under duress or over a shoulder.
 */
class KeyHolderKey(
    private val alias: String = DEFAULT_ALIAS,
    /**
     * Whether the key requires a recent biometric authentication.
     * Always true in the app. The instrumented tests set it to false,
     * because no emulator can present a fingerprint, and a test that
     * cannot run proves nothing; everything else about the key and the
     * ECDH path is identical either way.
     */
    private val requireUserAuthentication: Boolean = true,
) {

    private val keyStore: KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    /** Whether a long-term key has been generated on this phone yet. */
    fun exists(): Boolean = keyStore.containsAlias(alias)

    /**
     * Generates the long-term key if it does not exist yet, and reports
     * where it ended up. StrongBox is attempted first and its absence
     * is not an error: most phones do not have it, and a TEE-backed key
     * is still non-exportable.
     */
    fun ensureKey(): Backing {
        if (!exists()) {
            generate()
        }
        return backing()
    }

    private fun generate() {
        try {
            generateAttemptingStrongBox()
        } catch (e: IllegalStateException) {
            // The Keystore refuses to create an authentication-bound
            // key on a phone with no biometric enrolled at all. That is
            // a thing the user can fix, not a bug, so it must not reach
            // the screen as a raw exception message.
            throw BiometricEnrolmentRequired(e)
        }
    }

    private fun generateAttemptingStrongBox() {
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        try {
            generator.initialize(spec(strongBox = true))
            generator.generateKeyPair()
        } catch (e: StrongBoxUnavailableException) {
            // No secure element on this phone. Clean up any partial
            // entry before retrying, so a half-created alias cannot be
            // mistaken for a usable key later.
            if (keyStore.containsAlias(alias)) {
                keyStore.deleteEntry(alias)
            }
            generator.initialize(spec(strongBox = false))
            generator.generateKeyPair()
        }
        keyStore.load(null)
    }

    private fun spec(strongBox: Boolean): KeyGenParameterSpec =
        KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
            .setAlgorithmParameterSpec(ECGenParameterSpec(CURVE))
            .setUserAuthenticationRequired(requireUserAuthentication)
            .apply {
                if (requireUserAuthentication) {
                    setUserAuthenticationParameters(
                        AUTH_VALIDITY_SECONDS,
                        KeyProperties.AUTH_BIOMETRIC_STRONG,
                    )
                    // Enrolling a new fingerprint or face must not
                    // silently extend who can unlock the machine.
                    // Invalidating the key means re-pairing, which is
                    // the honest outcome: the machine's enrolment names
                    // this key, and this key is gone.
                    setInvalidatedByBiometricEnrollment(true)
                }
            }
            .setIsStrongBoxBacked(strongBox)
            .build()

    /** Where the private scalar actually lives. */
    enum class Backing { STRONGBOX, TEE_OR_SOFTWARE }

    fun backing(): Backing {
        val private = privateKey()
        val info = KeyFactory.getInstance(private.algorithm, ANDROID_KEYSTORE)
            .getKeySpec(private, KeyInfo::class.java)
        return if (info.securityLevel == KeyProperties.SECURITY_LEVEL_STRONGBOX) {
            Backing.STRONGBOX
        } else {
            Backing.TEE_OR_SOFTWARE
        }
    }

    /**
     * The long-term public point S, SEC1 uncompressed. This is what the
     * machine is enrolled against: `unlocker enrol --pubkey <hex>`.
     */
    fun publicPoint(): ByteArray {
        val public = keyStore.getCertificate(alias).publicKey as ECPublicKey
        val x = public.w.affineX.toFixedWidth(COORD_LEN)
        val y = public.w.affineY.toFixedWidth(COORD_LEN)
        return byteArrayOf(0x04) + x + y
    }

    /**
     * kid = sha256(S), which is how a machine names which enrolled key
     * holder a recovery request is for.
     */
    fun kid(): ByteArray = MessageDigest.getInstance("SHA-256").digest(publicPoint())

    /**
     * The whole of this key holder's contribution to a recovery: the
     * x-coordinate of s.X, where X is the machine's challenge point
     * given as its two raw affine coordinates.
     *
     * The peer key is rebuilt here from raw coordinates rather than
     * parsed from an encoded key, because the machine builds X by curve
     * addition (X = C + E) and no KeyPairGenerator ever produced it.
     * That the Keystore accepts such a point at all is the finding WP0
     * exists to have established.
     *
     * Throws android.security.keystore.UserNotAuthenticatedException
     * when the biometric window has expired, which the caller should
     * treat as "ask again", not as a protocol failure.
     */
    fun agree(peerX: ByteArray, peerY: ByteArray): ByteArray {
        require(peerX.size == COORD_LEN && peerY.size == COORD_LEN) {
            "challenge point coordinates must be $COORD_LEN bytes each"
        }

        // Reuse our own key's curve parameters rather than looking
        // secp256r1 up again: it guarantees the peer point is
        // interpreted on exactly the curve the private key lives on.
        val ourPublic = keyStore.getCertificate(alias).publicKey as ECPublicKey
        val peerPoint = ECPoint(peerX.toPositiveBigInteger(), peerY.toPositiveBigInteger())
        val peerPublic = KeyFactory.getInstance("EC")
            .generatePublic(ECPublicKeySpec(peerPoint, ourPublic.params))

        val agreement = KeyAgreement.getInstance("ECDH")
        agreement.init(privateKey())
        agreement.doPhase(peerPublic, true)
        val secret = agreement.generateSecret()
        check(secret.size == COORD_LEN) {
            "expected a $COORD_LEN-byte x-coordinate, got ${secret.size} bytes"
        }
        return secret
    }

    /**
     * Deletes the long-term key. Every machine enrolled against it
     * becomes unrecoverable through this phone, so the UI only offers
     * this behind an explicit confirmation.
     */
    fun delete() {
        if (keyStore.containsAlias(alias)) {
            keyStore.deleteEntry(alias)
        }
    }

    private fun privateKey(): PrivateKey =
        keyStore.getKey(alias, null) as? PrivateKey
            ?: throw IllegalStateException("no key holder key under alias $alias")

    companion object {
        const val DEFAULT_ALIAS = "synctang-key-holder"

        /**
         * How long a biometric authentication stays good for. Long
         * enough to survive the round trip through the prompt and the
         * network write, short enough that it cannot be banked.
         */
        const val AUTH_VALIDITY_SECONDS = 15

        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val CURVE = "secp256r1"
        private const val COORD_LEN = 32
    }
}

/**
 * The phone has no biometric enrolled, so the Keystore will not create
 * a key that requires one. Not a failure of this app: the user can fix
 * it in the phone's own settings, and the UI says so.
 */
class BiometricEnrolmentRequired(cause: Throwable) :
    Exception("no biometric is enrolled on this phone", cause)

/**
 * BigInteger.toByteArray() emits a two's complement encoding: a leading
 * zero byte when the top bit is set, and fewer than [width] bytes for a
 * small value. Neither is what a fixed-width curve coordinate is.
 */
internal fun java.math.BigInteger.toFixedWidth(width: Int): ByteArray {
    val raw = toByteArray()
    return when {
        raw.size == width -> raw
        raw.size == width + 1 && raw[0] == 0.toByte() -> raw.copyOfRange(1, raw.size)
        raw.size < width -> ByteArray(width - raw.size) + raw
        else -> throw IllegalArgumentException("value does not fit in $width bytes")
    }
}

/** Reads bytes as an unsigned big-endian integer. */
internal fun ByteArray.toPositiveBigInteger(): java.math.BigInteger =
    java.math.BigInteger(1, this)
