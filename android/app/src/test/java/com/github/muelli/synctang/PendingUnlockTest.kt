// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * What a shortcut or a widget hands to the app: the Device ID of the machine to unlock, kept in the process
 * for a moment, taken once, and never taken from anything but the app's own entry activity.
 */
class PendingUnlockTest {
    private var now = 1_000L
    private val pending = PendingUnlock { now }
    private val id = "AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA"

    @Test
    fun whatWasSetIsTakenOnce() {
        pending.set(id)
        assertEquals(id, pending.take())
        assertNull(pending.take())
    }

    @Test
    fun nothingSetMeansNothingTaken() {
        assertNull(pending.take())
    }

    @Test
    fun aRequestThatIsTooOldIsDropped() {
        pending.set(id)
        now += PENDING_UNLOCK_MILLIS + 1
        assertNull(pending.take())
    }

    @Test
    fun theLastRequestWins() {
        pending.set("BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB")
        pending.set(id)
        assertEquals(id, pending.take())
    }

    @Test
    fun onlyAnIdThatLooksLikeADeviceIdIsKept() {
        for (bad in listOf("", " ", "x".repeat(200), "AAAAAAA-AAAAAAA\nrm -rf", "../../etc/passwd", "aaaaaaa-aaaaaaa-aaaaaaa-aaaaaaa-aaaaaaa-aaaaaaa-aaaaaaa-aaaaaaa")) {
            pending.set(bad)
            assertNull("kept: $bad", pending.take())
        }
    }
}
