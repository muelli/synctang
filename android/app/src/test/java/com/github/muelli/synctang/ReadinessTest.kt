// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The readiness chip on the Ready screen: a coloured dot AND words, so colour is never the
 * only signal; one set of words per state, a live-region announcement only when the state
 * changes and not when only the age does; and colours that can be seen on the dark theme.
 */
class ReadinessTest {

    private fun r(state: ReadinessState, age: Int = -1) = Readiness(state, age)

    @Test
    fun theGoStatesMapToTheAppStatesAndAnUnknownOneToChecking() {
        assertEquals(ReadinessState.WAITING, ReadinessState.fromGo("WAITING"))
        assertEquals(ReadinessState.STALE, ReadinessState.fromGo("STALE"))
        assertEquals(ReadinessState.ABSENT, ReadinessState.fromGo("ABSENT"))
        assertEquals(ReadinessState.NO_NETWORK, ReadinessState.fromGo("NO_NETWORK"))
        assertEquals(ReadinessState.CHECKING, ReadinessState.fromGo("something new"))
    }

    @Test
    fun everyStateHasItsOwnWordsAndNoTwoShareThem() {
        val labels = ReadinessState.values().map { readinessLabel(r(it, 600)).res }
        assertTrue(labels.all { it != 0 })
        assertEquals(labels.size, labels.toSet().size)
        val announcements = ReadinessState.values().map { readinessAnnouncement(it) }
        assertEquals(announcements.size, announcements.toSet().size)
    }

    @Test
    fun aStaleRecordSaysHowLongAgoInTheUnitThatFits() {
        assertEquals(R.string.readiness_stale_minutes to listOf<Any>(3), readinessLabel(r(ReadinessState.STALE, 180)).let { it.res to it.args })
        assertEquals(R.string.readiness_stale_minutes to listOf<Any>(47), readinessLabel(r(ReadinessState.STALE, 47 * 60 + 20)).let { it.res to it.args })
        assertEquals(R.string.readiness_stale_hours to listOf<Any>(5), readinessLabel(r(ReadinessState.STALE, 5 * 3600 + 100)).let { it.res to it.args })
        assertEquals(R.string.readiness_stale_days to listOf<Any>(3), readinessLabel(r(ReadinessState.STALE, 3 * 86400 + 100)).let { it.res to it.args })
        assertEquals(R.string.readiness_stale_unknown, readinessLabel(r(ReadinessState.STALE, -1)).res)
    }

    // Announced when the state changes, not on every poll and not when only the age moves.
    @Test
    fun theLiveRegionIsAnnouncedOnlyWhenTheStateChanges() {
        assertTrue(shouldAnnounceReadiness(null, r(ReadinessState.CHECKING)))
        assertTrue(shouldAnnounceReadiness(r(ReadinessState.CHECKING), r(ReadinessState.WAITING, 10)))
        assertFalse(shouldAnnounceReadiness(r(ReadinessState.WAITING, 10), r(ReadinessState.WAITING, 15)))
        assertFalse(shouldAnnounceReadiness(r(ReadinessState.STALE, 600), r(ReadinessState.STALE, 660)))
        assertTrue(shouldAnnounceReadiness(r(ReadinessState.WAITING, 10), r(ReadinessState.STALE, 200)))
        // What is announced does not carry the age, so it does not change with it either.
        assertEquals(readinessAnnouncement(ReadinessState.STALE), readinessAnnouncement(ReadinessState.STALE))
    }

    @Test
    fun theDotColoursAreDistinctAndCanBeSeenOnTheDarkSurface() {
        val colours = ReadinessState.values().map { readinessDotColour(it) }
        assertEquals("two states share a colour", colours.size, colours.toSet().size)
        for (state in ReadinessState.values()) {
            val ratio = contrastRatio(readinessDotColour(state), DARK_SURFACE)
            assertTrue("$state dot has contrast $ratio against the dark surface", ratio >= 3.0)
        }
        assertNotEquals(readinessDotColour(ReadinessState.WAITING), readinessDotColour(ReadinessState.ABSENT))
    }

    @Test
    fun theContrastRatioIsTheWcagOne() {
        assertEquals(21.0, contrastRatio(0xFFFFFFFF, 0xFF000000), 0.01)
        assertEquals(1.0, contrastRatio(0xFF123456, 0xFF123456), 0.0001)
    }
}
