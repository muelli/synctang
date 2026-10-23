// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The scanner decodes about thirty frames a second, so a code held in
 * front of the camera must reach the caller once, not thirty times. A
 * code the caller rejects must not end scanning, or one bad code
 * leaves the screen showing an error and a camera that never looks
 * again.
 */
class ScanLatchTest {

    private var now = 0L
    private val latch = ScanLatch { now }

    @Test
    fun acceptedLatchesForEver() {
        assertTrue(latch.offer("a"))
        latch.accepted()
        assertFalse(latch.offer("b"))
        now += 10_000
        assertFalse(latch.offer("a"))
    }

    @Test
    fun rejectedReArms() {
        assertTrue(latch.offer("a"))
        latch.rejected()
        assertTrue(latch.offer("b"))
    }

    @Test
    fun sameTextWithinDebounceIsIgnored() {
        assertTrue(latch.offer("a"))
        latch.rejected()
        now = 1999
        assertFalse(latch.offer("a"))
    }

    @Test
    fun sameTextAfterDebounceIsOfferedAgain() {
        assertTrue(latch.offer("a"))
        latch.rejected()
        now = 2000
        assertTrue(latch.offer("a"))
    }

    @Test
    fun differentTextIsOfferedAtOnce() {
        assertTrue(latch.offer("a"))
        latch.rejected()
        assertTrue(latch.offer("b"))
    }

    @Test
    fun onlyOneOfferIsInFlight() {
        assertTrue(latch.offer("a"))
        assertFalse(latch.offer("b"))
    }
}
