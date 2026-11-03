// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the Approve screen says about the time left. It is counted down to a moment
 * fixed when the connection was made, not from when the screen appeared, and shown
 * in steps of five seconds: a figure that changed every second would be noise, and
 * for a screen reader it would be read out every time.
 */
class ApproveWindowTest {

    private val expires = 100_000L

    @Test
    fun theTimeLeftIsRoundedUpToFiveSeconds() {
        assertEquals(100, approveTimeLeft(expires, now = 0L).seconds)
        assertEquals(100, approveTimeLeft(expires, now = 1L).seconds)
        assertEquals(95, approveTimeLeft(expires, now = 5_000L).seconds)
        assertEquals(100, approveTimeLeft(expires, now = 4_999L).seconds)
        assertEquals(20, approveTimeLeft(expires, now = 84_900L).seconds)
        assertEquals(15, approveTimeLeft(expires, now = 85_000L).seconds)
        assertEquals(5, approveTimeLeft(expires, now = 99_999L).seconds)
    }

    @Test
    fun theTimeLeftNeverGoesBelowZero() {
        assertEquals(0, approveTimeLeft(expires, now = 100_000L).seconds)
        assertEquals(0, approveTimeLeft(expires, now = 250_000L).seconds)
    }

    @Test
    fun itIsRunningOutFromFifteenSecondsLeftAndNotBefore() {
        assertFalse(approveTimeLeft(expires, now = 84_999L).runningOut)
        assertTrue(approveTimeLeft(expires, now = 85_000L).runningOut)
        assertTrue(approveTimeLeft(expires, now = 99_000L).runningOut)
        assertTrue(approveTimeLeft(expires, now = 120_000L).runningOut)
    }
}
