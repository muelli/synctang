// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A 56-character device ID or a 64-character key ID shown in full is
 * unreadable on a phone, so screens show a short form and keep the
 * full value one tap away.
 */
class FingerprintTest {

    private val id = "AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"

    @Test
    fun aDeviceIdKeepsItsFirstTwoGroupsAndItsLast() {
        assertEquals("AAAAAAA-BBBBBBB-…-HHHHHHH", shortDeviceId(id))
        assertEquals("aaaaaaa-bbbbbbb-…-hhhhhhh", shortDeviceId(id.lowercase()))
    }

    @Test
    fun anythingElseIsLeftAlone() {
        for (odd in listOf("", "hello", "AAAAAAA-BBBBBBB-CCCCCCC", id.replace("HHHHHHH", "HHHH"), id.replace('-', ' '))) {
            assertEquals(odd, shortDeviceId(odd))
        }
    }

    @Test
    fun aKeyIdShowsItsFirstSixteenDigitsInFours() {
        val kid = "0123456789abcdef" + "0".repeat(48)
        assertEquals("0123 4567 89ab cdef …", shortKeyId(kid))
        assertEquals("0123 4567 89", shortKeyId("0123456789"))
        assertEquals("0123 4567 89ab cdef", shortKeyId("0123456789abcdef"))
    }

    @Test
    fun aKeyIdThatIsNotHexIsLeftAlone() {
        assertEquals("", shortKeyId(""))
        assertEquals("not hex", shortKeyId("not hex"))
    }

    @Test
    fun deviceIdsCompareWithoutCaseOrDashes() {
        assertTrue(sameDeviceId(id, id.lowercase().replace("-", "")))
        assertTrue(sameDeviceId(id, " $id "))
        assertFalse(sameDeviceId(id, id.replace('H', 'J')))
        assertFalse(sameDeviceId("", ""))
        assertFalse(sameDeviceId(id, ""))
    }
}
