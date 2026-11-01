// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.security.PrivateKey
import java.security.PublicKey
import java.security.spec.MGF1ParameterSpec
import javax.crypto.Cipher
import javax.crypto.spec.OAEPParameterSpec
import javax.crypto.spec.PSource

/**
 * RSA-OAEP wrapping of the key holder's private scalar.
 *
 * Wrapping with a public key means the scalar can be wrapped when it is made,
 * with no fingerprint, while unwrapping needs the private key, which lives in the
 * phone's secure hardware and is released for one operation at a time by a
 * fingerprint. (An AES key that needed a fingerprint would need one to wrap as
 * well.)
 *
 * The digests are SHA-256 for OAEP and SHA-1 for MGF1. That odd pairing is not a
 * preference: the Android Keystore on a Keymaster 4 phone uses SHA-1 for the mask
 * whatever is asked for, and the two sides have to agree. MGF1 with SHA-1 is not
 * the weak point of OAEP; the hash that matters is the first one.
 */
internal object Oaep {

    const val TRANSFORMATION = "RSA/ECB/OAEPWithSHA-256AndMGF1Padding"

    /** The length of a P-256 scalar, and the only length that is ever wrapped. */
    const val SCALAR_LENGTH = 32

    private fun parameters() =
        OAEPParameterSpec("SHA-256", "MGF1", MGF1ParameterSpec.SHA1, PSource.PSpecified.DEFAULT)

    /** Wraps a 32-byte scalar for [publicKey]. */
    fun wrap(publicKey: PublicKey, scalar: ByteArray): ByteArray {
        require(scalar.size == SCALAR_LENGTH) { "a scalar is $SCALAR_LENGTH bytes, got ${scalar.size}" }
        return wrapUnchecked(publicKey, scalar)
    }

    /** Wraps anything. Only for a test that needs a blob of the wrong length. */
    internal fun wrapUnchecked(publicKey: PublicKey, bytes: ByteArray): ByteArray =
        Cipher.getInstance(TRANSFORMATION).run {
            init(Cipher.ENCRYPT_MODE, publicKey, parameters())
            doFinal(bytes)
        }

    /**
     * A cipher ready to decrypt with [privateKey], not yet used. This is what goes to
     * the biometric prompt: the prompt authenticates this one operation, and
     * [unwrap] then runs it.
     */
    fun decryptCipher(privateKey: PrivateKey): Cipher =
        Cipher.getInstance(TRANSFORMATION).apply { init(Cipher.DECRYPT_MODE, privateKey, parameters()) }

    /**
     * Runs the decryption on [cipher] and returns the scalar. The caller wipes it.
     * Anything that does not decrypt to exactly 32 bytes was not wrapped by this
     * code and is refused.
     */
    fun unwrap(cipher: Cipher, wrapped: ByteArray): ByteArray {
        val scalar = cipher.doFinal(wrapped)
        if (scalar.size != SCALAR_LENGTH) {
            wipe(scalar)
            throw IllegalStateException("the unwrapped key is ${scalar.size} bytes, not $SCALAR_LENGTH")
        }
        return scalar
    }

    fun wipe(bytes: ByteArray) = bytes.fill(0)
}
