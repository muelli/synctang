// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import android.security.keystore.UserNotAuthenticatedException
import java.security.GeneralSecurityException
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
 * KeyAgreement in the API this app compiles and runs against. Checked
 * against android.jar for API 34: CryptoObject accepts Signature,
 * Cipher, Mac, IdentityCredential and PresentationSession, and nothing
 * else. (A KeyAgreement constructor was added in API 36.1, and in
 * androidx.biometric 1.4.0-alpha07; with minSdk 31 it could only be
 * used where present, falling back to this window elsewhere. Not done
 * yet.) So the per-operation
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
        } catch (e: GeneralSecurityException) {
            // The Keystore refuses to create an authentication-bound
            // key on a phone with no biometric enrolled at all. That is
            // a thing the user can fix, not a bug, so it must not reach
            // the screen as a raw exception message.
            //
            // It does not raise the useful IllegalStateException to the
            // caller, though: it raises an
            // InvalidAlgorithmParameterException wrapping it. Catching
            // the inner type alone, which this used to do, meant the
            // mapping never ran and a fresh install on a phone with no
            // fingerprint showed the raw exception and a "Try again"
            // button that could only fail the same way.
            if (requireUserAuthentication && e.looksLikeMissingBiometricEnrolment()) {
                throw BiometricEnrolmentRequired(e)
            }
            throw e
        } catch (e: IllegalStateException) {
            throw BiometricEnrolmentRequired(e)
        }
    }

    private fun generateAttemptingStrongBox() {
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        try {
            generator.initialize(spec(strongBox = true))
            generator.generateKeyPair()
        } catch (e: StrongBoxUnavailableException) {
            // Logged, because the reason matters and was invisible: a phone
            // can advertise StrongBox and still refuse to hold this kind of
            // key (an ECDH key, PURPOSE_AGREE_KEY, is newer than the first
            // generations of the chip). The message carries no key material.
            android.util.Log.w(LOG_TAG, "StrongBox refused the key, falling back to the TEE", e)
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

    /**
     * Where Android says the key was made, which is not always where it was
     * asked for. SOFTWARE is a real answer here: on a phone whose secure
     * hardware predates key agreement (seen on a Pixel 3a XL, Android 12,
     * Keymaster 4.x) a PURPOSE_AGREE_KEY key is created in software even when
     * StrongBox is requested, while signing keys on the same phone are made
     * in the TEE and in StrongBox. Reported as such, never as hardware.
     */
    enum class Backing { STRONGBOX, TEE, SOFTWARE }

    fun backing(): Backing {
        val private = privateKey()
        val info = KeyFactory.getInstance(private.algorithm, ANDROID_KEYSTORE)
            .getKeySpec(private, KeyInfo::class.java)
        return classify(info.securityLevel)
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
     * Throws [AuthenticationExpired] when the biometric window has
     * expired, which the caller should treat as "ask again", not as a
     * protocol failure, and [KeyInvalidated] when the key has been
     * retired for good.
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
        val secret = try {
            agreement.init(privateKey())
            agreement.doPhase(peerPublic, true)
            agreement.generateSecret()
        } catch (e: KeyPermanentlyInvalidatedException) {
            throw KeyInvalidated(e)
        } catch (e: UserNotAuthenticatedException) {
            throw AuthenticationExpired(e)
        }
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
         * What a KeyInfo security level means for what the person is told.
         * "Secure, but not which" counts as the TEE; a level that is unknown,
         * or one this build has never heard of, is reported as software,
         * because the alternative is claiming hardware that nothing confirmed.
         */
        /**
         * Whether the app will not use a key that lives here. A software key
         * is not protected by the phone's secure hardware, which is the
         * protection this app tells people they have, so it is refused unless
         * the person has said, on this phone, that they accept that.
         */
        fun refuses(backing: Backing, softwareAllowed: Boolean): Boolean =
            backing == Backing.SOFTWARE && !softwareAllowed

        fun classify(securityLevel: Int): Backing = when (securityLevel) {
            KeyProperties.SECURITY_LEVEL_STRONGBOX -> Backing.STRONGBOX
            KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT,
            KeyProperties.SECURITY_LEVEL_UNKNOWN_SECURE -> Backing.TEE
            else -> Backing.SOFTWARE
        }

        /**
         * How long a biometric authentication stays good for. Between
         * the prompt returning and the key being used there is only local
         * work (the Go bridge handing over the challenge point and the
         * ECDH itself); the network write comes after the key has been
         * used. So this only has to cover a slow phone's local work, and
         * is kept to a few seconds because, while it is open, anything
         * running inside the app can use the key without a prompt, and
         * any fingerprint unlock of the phone opens it, not only this
         * app's prompt.
         *
         * It is fixed when the key is created. A key created before this
         * was shortened (it was 15) keeps its old window: replacing it
         * would change the public key and break every machine the phone
         * is enrolled on, so the app does not do that on its own. A new
         * install, or a key recreated after the biometrics changed, gets
         * this value.
         */
        const val AUTH_VALIDITY_SECONDS = 5

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
 * The phone made this key in software, and its owner has not accepted that.
 * Not a failure of the app: see [KeyHolderKey.refuses].
 */
class SoftwareKeyRefused :
    Exception("the key on this phone is software-backed and was not allowed")

/**
 * The key has been retired for good, which the Keystore does to a key
 * created with setInvalidatedByBiometricEnrollment the moment a
 * fingerprint or face is added to the phone. Nothing brings it back:
 * the phone needs a new key, and every machine a new enrolment.
 */
class KeyInvalidated(cause: Throwable) :
    Exception("this phone's key was invalidated by a change to its biometrics", cause)

/**
 * The biometric match is older than the key's authentication window,
 * so the Keystore refused the ECDH. Approving again opens a new one.
 */
class AuthenticationExpired(cause: Throwable) :
    Exception("the biometric authentication expired before the key was used", cause)

/**
 * Whether this failure is the Keystore refusing to create an
 * authentication-bound key because nothing is enrolled to
 * authenticate with.
 *
 * Decided on the cause chain's types rather than on the message text:
 * the wording belongs to whichever Android version is running and is
 * not a promise, while the shape (a parameter rejection caused by an
 * IllegalStateException) is what the platform actually documents
 * itself as doing. Walks the chain defensively, since a cause cycle
 * is legal enough to construct and an infinite loop here would hang
 * the first screen of the app.
 */
internal fun Throwable.looksLikeMissingBiometricEnrolment(): Boolean {
    var cause: Throwable? = this
    var seen = 0
    while (cause != null && seen < 16) {
        if (cause is IllegalStateException) {
            return true
        }
        cause = cause.cause
        seen++
    }
    return false
}

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
