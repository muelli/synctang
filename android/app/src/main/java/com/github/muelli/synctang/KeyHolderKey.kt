// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import java.io.File
import java.security.GeneralSecurityException
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.ProviderException
import javax.crypto.Cipher
import mobile.Mobile

/**
 * The key holder's long-term MR-1 key.
 *
 * ### Why the key is not simply a Keystore key
 *
 * MR-1 needs an elliptic curve key agreement. The Android Keystore offers one
 * (PURPOSE_AGREE_KEY), but only a KeyMint implementation can do it in hardware.
 * A phone with an older Keymaster (a Pixel 3a XL's, which has a Titan M chip and a
 * TrustZone) gets a key made by Android's own software fallback instead, and that
 * is no better than a file. So the scalar is not kept as a Keystore key.
 *
 * ### What is done instead
 *
 * The scalar is made in Go ([Mobile.newKeyScalar]) and wrapped at once with
 * RSA-OAEP under a public key whose private half is an Android Keystore key. That
 * private key lives in the phone's secure hardware (StrongBox where there is one,
 * the TEE otherwise), cannot be exported, and is created so that every single use
 * needs a strong biometric, bound to that operation through a
 * BiometricPrompt.CryptoObject. The wrapped scalar and the public point are kept in
 * a directory Android does not back up ([WrappedKeyStore]).
 *
 * To answer a request: [prepareUnwrap] gives a cipher, the biometric prompt
 * authenticates that cipher, [unwrap] returns the scalar, the Go side multiplies the
 * machine's challenge point by it and wipes its copy, and the caller wipes this one.
 *
 * ### What this protects and what it does not
 *
 * At rest the scalar is only ever wrapped, so a copy of the app's files, a backup
 * or a disk image holds nothing usable, and the wrapping key cannot leave the
 * hardware. Each use needs a fingerprint for that operation: there is no window that
 * any other fingerprint unlock could open (the time window the earlier design had to
 * use, because a key-agreement key cannot be bound to a prompt before Android 16
 * QPR2).
 *
 * What it does not protect is the scalar while it is in use: for the moment of one
 * answer it is in the app's memory, readable by a process with root access to a
 * running phone that waits for it. A non-exportable hardware key agreement key would
 * not have that weakness; this phone cannot offer one.
 */
class KeyHolderKey(
    directory: File,
    private val alias: String = DEFAULT_ALIAS,
    /**
     * Whether using the key requires a fresh strong biometric. Always true in the
     * app. The instrumented tests set it to false, because no emulator can present a
     * fingerprint, and a test that cannot run proves nothing; everything else
     * about the key is identical either way.
     */
    private val requireUserAuthentication: Boolean = true,
) {

    private val keyStore: KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
    private val files = WrappedKeyStore(directory, alias)

    /** Whether a complete key, hardware half and wrapped half, is there. */
    fun exists(): Boolean = keyStore.containsAlias(alias) && files.exists()

    private fun plan(): KeyPlan = keyPlan(hardwareHalf = keyStore.containsAlias(alias), files = files.state())

    /**
     * Makes the key if there is none yet, and reports where its hardware half
     * ended up. StrongBox is tried first and its absence is not an error: most phones
     * do not have it, and a TEE-backed key is just as non-exportable.
     *
     * A key that is half there is not cleared by the app. The Keystore reports
     * almost every error as "no such key", so a half that seems to be missing may
     * only have failed to answer, and deleting the other half would destroy a key
     * every enrolled machine depends on. [KeyNeedsReplacing] is thrown instead, and
     * the person decides ([delete] is theirs to ask for). Only what cannot hold
     * anything yet, nothing at all or an unfinished creation, is made afresh.
     */
    fun ensureKey(): Backing {
        when (val plan = plan()) {
            is KeyPlan.Keep -> Unit
            is KeyPlan.Create -> {
                delete()
                create()
            }
            is KeyPlan.Stop -> throw KeyNeedsReplacing(plan.problem)
        }
        return backing()
    }

    private fun create() {
        try {
            generateWrappingKey()
        } catch (e: GeneralSecurityException) {
            delete()
            // The Keystore refuses to create an authentication-bound key on a phone
            // with no biometric enrolled at all. That is a thing the person can fix,
            // not a bug, so it must not reach the screen as a raw exception message.
            // It does not raise the useful IllegalStateException itself: it raises an
            // InvalidAlgorithmParameterException wrapping it, so the cause chain has
            // to be read.
            if (requireUserAuthentication && e.looksLikeMissingBiometricEnrolment()) {
                throw BiometricEnrolmentRequired(e)
            }
            throw e
        } catch (e: IllegalStateException) {
            delete()
            throw BiometricEnrolmentRequired(e)
        }

        // Only the creation of the Keystore key is read as "no biometric": a failure
        // below, writing a file say, is its own failure and is reported as one.
        var scalar: ByteArray? = null
        try {
            scalar = Mobile.newKeyScalar()
            val publicPoint = Mobile.publicPointOf(scalar)
            val wrapped = Oaep.wrap(keyStore.getCertificate(alias).publicKey, scalar)
            files.save(wrapped, publicPoint)
        } catch (e: Exception) {
            delete()
            throw e
        } finally {
            scalar?.let(Oaep::wipe)
        }
    }

    /**
     * Makes the hardware half, StrongBox first and the TEE if StrongBox will not do,
     * and proves the key can be used before anything depends on it.
     *
     * Making a key and using one are different things. On a Pixel 3a XL the Titan M
     * will create a StrongBox key and report it at the StrongBox level, and then every
     * use of that key fails: the Keystore's key blob upgrade call comes back
     * BACKEND_BUSY, over and over, for as long as anyone waits. A key that cannot be
     * used would only be found at the first unlock, so each candidate is used once
     * here, with a time limit, and abandoned for the next if it does not answer.
     */
    private fun generateWrappingKey() {
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_RSA, ANDROID_KEYSTORE)
        var made = false
        try {
            generator.initialize(spec(strongBox = true))
            generator.generateKeyPair()
            made = canBeUsed("StrongBox")
        } catch (e: ProviderException) {
            // No secure element on this phone (StrongBoxUnavailableException, which
            // is one of these), or one that errors instead of saying so, or none that
            // will hold this kind of key. Logged because the reason matters. Only the
            // StrongBox attempt is read this way: the TEE's own failures stay fatal.
            android.util.Log.w(LOG_TAG, "StrongBox cannot hold the wrapping key", e)
        }
        if (!made) {
            // Clear the failed attempt first, so a half-made alias cannot be mistaken
            // for a usable key.
            if (keyStore.containsAlias(alias)) keyStore.deleteEntry(alias)
            android.util.Log.w(LOG_TAG, "making the wrapping key in the TEE")
            generator.initialize(spec(strongBox = false))
            generator.generateKeyPair()
            if (!canBeUsed("the TEE")) {
                if (keyStore.containsAlias(alias)) keyStore.deleteEntry(alias)
                throw SecureHardwareUnresponsive()
            }
        }
        keyStore.load(null)
    }

    /**
     * Starts one unwrapping with the key just made, bounded, and says whether the
     * Keystore answered. Nothing is decrypted: starting the operation is what fails on
     * a key whose hardware does not work.
     */
    private fun canBeUsed(where: String): Boolean =
        try {
            runWithin(TRIAL_SECONDS) { Oaep.decryptCipher(privateKey()) }
            true
        } catch (e: KeyUseTimedOut) {
            android.util.Log.w(LOG_TAG, "a key made in $where did not answer within ${TRIAL_SECONDS}s", e)
            false
        } catch (e: Exception) {
            android.util.Log.w(LOG_TAG, "a key made in $where cannot be used", e)
            false
        }

    private fun spec(strongBox: Boolean): KeyGenParameterSpec =
        KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_DECRYPT)
            .setKeySize(WRAPPING_KEY_BITS)
            // SHA-256 for OAEP; SHA-1 is the digest the Keystore uses for the mask
            // on a Keymaster 4 phone and has to be allowed for the two to agree.
            .setDigests(KeyProperties.DIGEST_SHA256, KeyProperties.DIGEST_SHA1)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_RSA_OAEP)
            .setUserAuthenticationRequired(requireUserAuthentication)
            .apply {
                if (requireUserAuthentication) {
                    // Zero seconds: authentication per use, for this operation only,
                    // through a CryptoObject. Not a window.
                    setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                    // Enrolling a new fingerprint or face must not silently extend who
                    // can unlock the machine. Invalidating the key means re-pairing,
                    // which is the honest outcome: the machine's enrolment names the
                    // public point of a key that can no longer be unwrapped.
                    setInvalidatedByBiometricEnrollment(true)
                }
            }
            .setIsStrongBoxBacked(strongBox)
            .build()

    /**
     * Where Android says the hardware half was made, which is not always where it
     * was asked for. SOFTWARE is a real answer (an emulator, a phone with no secure
     * hardware for this): the wrapping then protects nothing the file itself would
     * not, and the app says so and asks before using it.
     */
    enum class Backing { STRONGBOX, TEE, SOFTWARE }

    fun backing(): Backing = classify(securityLevel())

    private fun securityLevel(): Int {
        val private = privateKey()
        return KeyFactory.getInstance(private.algorithm, ANDROID_KEYSTORE)
            .getKeySpec(private, KeyInfo::class.java)
            .securityLevel
    }

    /**
     * The long-term public point S, SEC1 uncompressed. This is what the machine is
     * enrolled against: `unlocker enrol --pubkey <hex>`.
     */
    fun publicPoint(): ByteArray = files.loadPublic()

    /**
     * kid = sha256(S), which is how a machine names which enrolled key holder a
     * recovery request is for.
     */
    fun kid(): ByteArray = MessageDigest.getInstance("SHA-256").digest(publicPoint())

    /**
     * A cipher ready to unwrap the scalar, which goes to the biometric prompt as its
     * CryptoObject. Throws [KeyInvalidated] when the key has been retired for good,
     * which the Keystore does the moment a fingerprint or face is added, and which it
     * reports here, before any prompt.
     */
    fun prepareUnwrap(): Cipher =
        try {
            Oaep.decryptCipher(privateKey())
        } catch (e: KeyPermanentlyInvalidatedException) {
            throw KeyInvalidated(e)
        }

    /**
     * Unwraps the scalar with a cipher the biometric prompt has authenticated. The
     * caller hands it to the Go side and wipes it. Throws [KeyInvalidated] when the key
     * has been retired for good.
     */
    fun unwrap(cipher: Cipher): ByteArray =
        try {
            Oaep.unwrap(cipher, files.loadWrapped())
        } catch (e: KeyPermanentlyInvalidatedException) {
            throw KeyInvalidated(e)
        }

    /**
     * Deletes the key, both halves. Every machine enrolled against it becomes
     * unrecoverable through this phone, so the UI only reaches this through the
     * screen that says so and asks (and the app clears a key it has only just made).
     */
    fun delete() {
        if (keyStore.containsAlias(alias)) keyStore.deleteEntry(alias)
        files.delete()
    }

    private fun privateKey(): PrivateKey =
        keyStore.getKey(alias, null) as? PrivateKey
            ?: throw IllegalStateException("no key holder key under alias $alias")

    companion object {
        const val DEFAULT_ALIAS = "synctang-key-holder"

        /** 2048 is the one RSA size every StrongBox implementation has to offer. */
        private const val WRAPPING_KEY_BITS = 2048

        /** How long a new key gets to start one operation before it is written off. */
        private const val TRIAL_SECONDS = 8L

        /**
         * What a KeyInfo security level means for what the person is told. "Secure, but
         * not which" counts as the TEE; a level that is unknown, or one this build has
         * never heard of, is reported as software, because the alternative is claiming
         * hardware that nothing confirmed.
         */
        fun classify(securityLevel: Int): Backing = when (securityLevel) {
            KeyProperties.SECURITY_LEVEL_STRONGBOX -> Backing.STRONGBOX
            KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT,
            KeyProperties.SECURITY_LEVEL_UNKNOWN_SECURE -> Backing.TEE
            else -> Backing.SOFTWARE
        }

        /**
         * Whether the app will not use a key whose hardware half is only software. The
         * wrapping then protects nothing the file itself would not, which is not what
         * the person is told they have, so it is refused unless they have said, on this
         * phone, that they accept that.
         */
        fun refuses(backing: Backing, softwareAllowed: Boolean): Boolean =
            backing == Backing.SOFTWARE && !softwareAllowed

        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
    }
}

/**
 * The phone made the hardware half of the key in software, and its owner has not
 * accepted that. Not a failure of the app: see [KeyHolderKey.refuses].
 */
class SoftwareKeyRefused :
    Exception("the key on this phone is software-backed and was not allowed")

/**
 * No secure hardware on this phone will answer for the key just made, neither
 * StrongBox nor the TEE. The key is removed; there is nothing the app can do but say
 * so.
 */
class SecureHardwareUnresponsive :
    Exception("the phone's secure hardware did not answer for the new key")

/**
 * The phone has no biometric enrolled, so the Keystore will not create a key that
 * requires one. Not a failure of this app: the user can fix it in the phone's own
 * settings, and the UI says so.
 */
class BiometricEnrolmentRequired(cause: Throwable) :
    Exception("no biometric is enrolled on this phone", cause)

/**
 * The key has been retired for good, which the Keystore does to a key created with
 * setInvalidatedByBiometricEnrollment the moment a fingerprint or face is added to
 * the phone. Nothing brings it back: the phone needs a new key, and every machine a
 * new enrolment.
 */
class KeyInvalidated(cause: Throwable) :
    Exception("this phone's key was invalidated by a change to its biometrics", cause)

/**
 * Whether this failure is the Keystore refusing to create an authentication-bound key
 * because nothing is enrolled to authenticate with.
 *
 * Decided on the cause chain's types rather than on the message text: the wording
 * belongs to whichever Android version is running and is not a promise, while the
 * shape (a parameter rejection caused by an IllegalStateException) is what the
 * platform actually documents itself as doing. Walks the chain defensively, since a
 * cause cycle is legal enough to construct and an infinite loop here would hang the
 * first screen of the app.
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

/** What is wrong with a key that is not whole. */
enum class KeyProblem {
    /** The wrapped key is there and the Keystore does not say it has the key that unwraps it. */
    HARDWARE_HALF_MISSING,

    /** A file is missing, empty or not what it should be. */
    FILES_DAMAGED,

    /** Both files are there, and the saved key is not the one the public point belongs to. */
    FILES_DO_NOT_MATCH,
}

/**
 * The key is not whole and the app will not decide what to do about it: see
 * [KeyHolderKey.ensureKey].
 */
class KeyNeedsReplacing(val problem: KeyProblem) : Exception("the key holder key is not whole: $problem")

sealed interface KeyPlan {
    /** A whole key is there. */
    object Keep : KeyPlan

    /** There is nothing that could be lost: make one. */
    object Create : KeyPlan

    /** Something that may be needed is missing or damaged. */
    data class Stop(val problem: KeyProblem) : KeyPlan
}

/**
 * What to do given whether the Keystore has the hardware half, which is only
 * trusted when it says yes, and what the files are.
 */
fun keyPlan(hardwareHalf: Boolean, files: FilesState): KeyPlan = when (files) {
    FilesState.DAMAGED -> KeyPlan.Stop(KeyProblem.FILES_DAMAGED)
    FilesState.COMPLETE ->
        if (hardwareHalf) KeyPlan.Keep else KeyPlan.Stop(KeyProblem.HARDWARE_HALF_MISSING)
    FilesState.ABSENT, FilesState.INCOMPLETE -> KeyPlan.Create
}
