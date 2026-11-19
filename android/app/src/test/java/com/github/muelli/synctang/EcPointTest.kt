// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.security.KeyPairGenerator
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import javax.crypto.KeyAgreement
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The machine speaks in SEC1 points (65 bytes, 0x04, x, y) and the Keystore in java.security keys. A key agreement that
 * goes through them must give the secret the Go side computes: the x-coordinate of the shared point, 32 bytes, and
 * the two directions of an agreement must agree, which is what these check with software keys.
 */
class EcPointTest {
    private fun pair() = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()

    @Test
    fun aPublicKeyIsSixtyFiveBytesStartingWithFour() {
        val point = sec1Of(pair().public as ECPublicKey)
        assertEquals(65, point.size)
        assertEquals(4, point[0].toInt())
    }

    @Test
    fun aPointGoesToAKeyAndBackUnchangedWhateverItsLeadingZeros() {
        repeat(200) {
            val key = pair().public as ECPublicKey
            val point = sec1Of(key)
            assertArrayEquals(point, sec1Of(ecPublicKeyFromSec1(point)))
        }
    }

    @Test
    fun twoSidesThatGoThroughPointsGetTheSameSharedSecret() {
        val a = pair()
        val b = pair()
        // a answers a challenge b sent as a point, and b works it out from the point a made: x(a . B) == x(b . A).
        val fromA = KeyAgreement.getInstance("ECDH").apply { init(a.private); doPhase(ecPublicKeyFromSec1(sec1Of(b.public as ECPublicKey)), true) }.generateSecret()
        val fromB = KeyAgreement.getInstance("ECDH").apply { init(b.private); doPhase(ecPublicKeyFromSec1(sec1Of(a.public as ECPublicKey)), true) }.generateSecret()
        assertEquals(32, fromA.size)
        assertArrayEquals(fromA, fromB)
        assertNotEquals(0, fromA.count { it.toInt() != 0 })
    }

    @Test
    fun whatIsNotAPointIsRefused() {
        for (bad in listOf(ByteArray(0), ByteArray(64), ByteArray(65) { 1 }, ByteArray(33), ByteArray(65).also { it[0] = 4 }.copyOf(66))) {
            val refused = runCatching { ecPublicKeyFromSec1(bad) }.isFailure
            assertTrue("accepted ${bad.size} bytes", refused)
        }
    }
}
