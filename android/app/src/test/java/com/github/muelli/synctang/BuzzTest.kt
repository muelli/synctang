// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * When the phone buzzes about a machine: once it has gone from a state that was seen not to be
 * ready to the one that is. Not for the first answer after a screen comes up (the person is
 * looking at it), and not for staying ready.
 */
class BuzzTest {

    @Test
    fun aMachineThatWasSeenNotReadyAndIsNowWaitingBuzzes() {
        for (before in listOf(ReadinessState.STALE, ReadinessState.ABSENT, ReadinessState.NO_NETWORK)) {
            assertTrue("$before", buzzForReadiness(before, ReadinessState.WAITING))
        }
    }

    @Test
    fun theFirstAnswerDoesNotBuzz() {
        assertFalse(buzzForReadiness(null, ReadinessState.WAITING))
        assertFalse(buzzForReadiness(ReadinessState.CHECKING, ReadinessState.WAITING))
    }

    @Test
    fun stayingReadyDoesNotBuzz() {
        assertFalse(buzzForReadiness(ReadinessState.WAITING, ReadinessState.WAITING))
    }

    @Test
    fun anythingButReadyDoesNotBuzz() {
        for (now in listOf(ReadinessState.CHECKING, ReadinessState.STALE, ReadinessState.ABSENT, ReadinessState.NO_NETWORK)) {
            for (before in listOf<ReadinessState?>(null) + ReadinessState.values()) {
                assertFalse("$before to $now", buzzForReadiness(before, now))
            }
        }
    }
}
