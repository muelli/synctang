// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Android drops multicast packets before they reach an application
 * unless a MulticastLock is held, so local discovery on the phone is
 * entirely dependent on this lock being held for the whole dial and
 * released afterwards: holding it longer than necessary keeps the
 * Wi-Fi chip out of its power-saving filter and costs battery.
 */
class MulticastLeaseTest {

    private class FakeLock : MulticastLease.Lock {
        var acquisitions = 0
        var releases = 0
        val held: Boolean get() = acquisitions > releases

        override fun acquire() {
            acquisitions++
        }

        override fun release() {
            releases++
        }
    }

    @Test
    fun theLockIsHeldForTheBodyAndReleasedAfterwards() {
        val lock = FakeLock()
        var heldDuringBody = false

        val result = MulticastLease.holding(lock) {
            heldDuringBody = lock.held
            "dialled"
        }

        assertEquals("dialled", result)
        assertTrue("the lock must be held while the dial runs", heldDuringBody)
        assertFalse("the lock must not still be held afterwards", lock.held)
        assertEquals(1, lock.acquisitions)
        assertEquals(1, lock.releases)
    }

    /**
     * A failed dial is the common case, not the exceptional one: the
     * machine may not be up yet. Leaking the lock on that path would
     * leave the Wi-Fi chip receiving all multicast traffic until the
     * process died.
     */
    @Test
    fun theLockIsReleasedWhenTheDialFails() {
        val lock = FakeLock()

        try {
            MulticastLease.holding(lock) {
                throw IllegalStateException("no route to host")
            }
            throw AssertionError("the exception should have propagated")
        } catch (expected: IllegalStateException) {
            // The caller still has to see why the dial failed.
            assertEquals("no route to host", expected.message)
        }

        assertFalse("a failed dial must still release the lock", lock.held)
        assertEquals(1, lock.releases)
    }

    /**
     * A device with no Wi-Fi service, or one that refuses the lock,
     * must still be able to dial over the relay. Local discovery is an
     * addition to the relay path, never a precondition for it.
     */
    @Test
    fun theDialStillRunsWithoutALock() {
        var ran = false

        val result = MulticastLease.holding(null) {
            ran = true
            "dialled anyway"
        }

        assertTrue("a missing lock must not stop the dial", ran)
        assertEquals("dialled anyway", result)
    }
}
