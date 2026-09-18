// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import mobile.Mobile
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * WP5, phase 1: the gomobile binding of this project's own Go code,
 * proved on a device against the fixed MR-1 vectors in
 * testdata/mr1.json.
 *
 * This runs the real mrcore arithmetic, cross-compiled for Android and
 * called through JNI, over exactly the numbers mrcore's own Go tests
 * use. If the binding, the cross-compilation or the byte-slice
 * marshalling were wrong in any way that mattered, the recovered
 * secret would not match.
 *
 * The vectors file is copied into this test APK's assets by the
 * copyMr1Vectors Gradle task, so the numbers are never transcribed by
 * hand and cannot drift from the Go side's copy.
 */
@RunWith(AndroidJUnit4::class)
class Mr1VectorsTest {

    private val vectors: JSONObject by lazy {
        val raw = InstrumentationRegistry.getInstrumentation().context.assets
            .open("mr1.json")
            .use { it.readBytes() }
            .toString(Charsets.UTF_8)
        JSONObject(raw)
    }

    private fun hex(section: String, key: String): ByteArray =
        vectors.getJSONObject(section).getString(key).decodeHex()

    private fun recipientFromVectors() = Mobile.newRecipient().apply {
        kid = hex("enrol", "kid")
        s = hex("recipient", "S")
        c = hex("enrol", "C")
        ciphertext = hex("enrol", "ct")
        nonce = hex("enrol", "nonce")
    }

    @Test
    fun xOnlyRecoveryMatchesTheFixedVector() {
        val recovered = recipientFromVectors().finishXOnly(
            hex("recover", "e_scalar"),
            hex("recover", "xOnly"),
        )
        assertArrayEquals(
            "the secret recovered through the gomobile binding must equal the vector's P",
            hex("enrol", "P"),
            recovered,
        )
    }

    @Test
    fun aWrongXCoordinateIsRefusedRatherThanReturningRubbish() {
        val wrong = hex("recover", "xOnly")
        wrong[0] = (wrong[0].toInt() xor 0xff).toByte()
        var threw = false
        try {
            recipientFromVectors().finishXOnly(hex("recover", "e_scalar"), wrong)
        } catch (e: Exception) {
            threw = true
        }
        assertTrue("a wrong x-coordinate must be an error, not a wrong secret", threw)
    }

    @Test
    fun affineCoordinatesSplitTheChallengePointAsSec1Says() {
        val point = hex("recover", "X")
        val x = Mobile.affineX(point)
        val y = Mobile.affineY(point)

        assertEquals(32, x.size)
        assertEquals(32, y.size)
        assertArrayEquals(point.copyOfRange(1, 33), x)
        assertArrayEquals(point.copyOfRange(33, 65), y)
    }

    @Test
    fun deviceIdIsDerivedDeterministicallyFromTheIdentitySeed() {
        val a = Mobile.deviceIDForSeed("a fixed test seed")
        val b = Mobile.deviceIDForSeed("a fixed test seed")
        val other = Mobile.deviceIDForSeed("a different test seed")

        assertEquals("the same seed must always yield the same identity", a, b)
        assertNotEquals("different seeds must yield different identities", a, other)
        // Syncthing Device IDs are 7 groups of 7 base32 characters,
        // separated by dashes.
        assertTrue("unexpected Device ID shape: $a", Regex("^[A-Z2-7]{7}(-[A-Z2-7]{7}){7}$").matches(a))
    }
}

internal fun String.decodeHex(): ByteArray {
    require(length % 2 == 0) { "hex string of odd length" }
    return ByteArray(length / 2) { substring(it * 2, it * 2 + 2).toInt(16).toByte() }
}
