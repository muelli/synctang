// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.security.KeyPairGenerator
import java.security.SecureRandom
import javax.crypto.BadPaddingException
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.BeforeClass
import org.junit.Test

/**
 * The wrapping itself, with an ordinary RSA key standing in for the hardware
 * one. What these cannot show is the Android Keystore's own behaviour (the
 * hardware key, the fingerprint, the MGF1 digest it insists on); that is
 * checked on a phone.
 */
class OaepWrappingTest {

    companion object {
        private lateinit var pair: java.security.KeyPair
        private lateinit var other: java.security.KeyPair

        @JvmStatic
        @BeforeClass
        fun keys() {
            val gen = KeyPairGenerator.getInstance("RSA").apply { initialize(2048) }
            pair = gen.generateKeyPair()
            other = gen.generateKeyPair()
        }
    }

    private fun scalar() = ByteArray(32).also { SecureRandom().nextBytes(it) }

    @Test
    fun aWrappedScalarComesBackUnchanged() {
        val s = scalar()
        val wrapped = Oaep.wrap(pair.public, s)
        assertArrayEquals(s, Oaep.unwrap(Oaep.decryptCipher(pair.private), wrapped))
    }

    @Test
    fun theWrappedKeyIsAFullRsaBlockAndDiffersEachTime() {
        val s = scalar()
        val a = Oaep.wrap(pair.public, s)
        val b = Oaep.wrap(pair.public, s)
        assertEquals(256, a.size)
        assertNotEquals("OAEP is randomised: the same scalar must not wrap the same way twice", a.toList(), b.toList())
    }

    @Test
    fun theWrappedKeyDoesNotContainTheScalar() {
        val s = scalar()
        val wrapped = Oaep.wrap(pair.public, s)
        assertTrue(wrapped.toList().windowed(s.size).none { it == s.toList() })
    }

    @Test
    fun anotherKeyCannotUnwrapIt() {
        val wrapped = Oaep.wrap(pair.public, scalar())
        try {
            Oaep.unwrap(Oaep.decryptCipher(other.private), wrapped)
            fail("a different key unwrapped it")
        } catch (e: BadPaddingException) {
            // expected
        }
    }

    @Test
    fun aTamperedWrappedKeyIsRefusedNotUnwrappedToSomethingElse() {
        val wrapped = Oaep.wrap(pair.public, scalar())
        wrapped[100] = (wrapped[100].toInt() xor 1).toByte()
        try {
            Oaep.unwrap(Oaep.decryptCipher(pair.private), wrapped)
            fail("a tampered blob was unwrapped")
        } catch (e: BadPaddingException) {
            // expected
        }
    }

    // Whatever was wrapped, only 32 bytes are a P-256 scalar.
    @Test
    fun somethingThatDecryptsToTheWrongLengthIsRefused() {
        val wrapped = Oaep.wrapUnchecked(pair.public, ByteArray(31) { 1 })
        try {
            Oaep.unwrap(Oaep.decryptCipher(pair.private), wrapped)
            fail("accepted a 31-byte scalar")
        } catch (e: IllegalStateException) {
            // expected
        }
    }

    @Test
    fun wrappingRefusesAScalarOfTheWrongLength() {
        try {
            Oaep.wrap(pair.public, ByteArray(31))
            fail("wrapped 31 bytes")
        } catch (e: IllegalArgumentException) {
            // expected
        }
    }

    @Test
    fun theTransformationIsOaepWithSha256() {
        assertEquals("RSA/ECB/OAEPWithSHA-256AndMGF1Padding", Oaep.decryptCipher(pair.private).algorithm)
    }

    @Test
    fun wipingClearsTheBytes() {
        val s = scalar()
        Oaep.wipe(s)
        assertArrayEquals(ByteArray(32), s)
    }
}
