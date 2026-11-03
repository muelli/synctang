// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the busy screen says while it waits is chosen from how far the Go side has
 * got. These pin the mapping: every stage can be said in words, no two stages say
 * the same, and the numbers agree with mobile/progress.go.
 */
class ProgressTest {

    // The numbers of mobile/progress.go, written out so that renumbering either side
    // fails here. (ProgressConstantsTest in the instrumented tests checks them against
    // the generated binding itself.)
    private val goNumbers = mapOf(
        Stage.NONE to 0L,
        Stage.STARTING to 1L,
        Stage.LOOKING_UP to 2L,
        Stage.CONNECTING to 3L,
        Stage.CHECKING_IDENTITY to 4L,
        Stage.WAITING_FOR_REQUEST to 5L,
        Stage.REQUEST_READY to 6L,
        Stage.SENDING_ANSWER to 7L,
        Stage.WAITING_FOR_VERDICT to 8L,
        Stage.UNLOCKED to 9L,
        Stage.SENDING_PROOF to 10L,
        Stage.WAITING_FOR_OPERATOR to 11L,
        Stage.ENROLLED to 12L,
    )

    @Test
    fun everyStageHasAGoNumberAndComesBackFromIt() {
        assertEquals(Stage.values().toSet(), goNumbers.keys)
        for ((stage, n) in goNumbers) {
            assertEquals(stage, Stage.fromGo(n))
            assertEquals(n, stage.goNumber)
        }
    }

    @Test
    fun aNumberTheAppDoesNotKnowIsNoStageRatherThanACrash() {
        assertEquals(Stage.NONE, Stage.fromGo(99))
        assertEquals(Stage.NONE, Stage.fromGo(-1))
        assertEquals(Route.UNKNOWN, Route.fromGo(42))
    }

    @Test
    fun theRouteNumbersAreThoseOfTheGoSide() {
        assertEquals(Route.UNKNOWN, Route.fromGo(0))
        assertEquals(Route.RELAY, Route.fromGo(1))
        assertEquals(Route.LOCAL, Route.fromGo(2))
        assertEquals(Route.DIRECT, Route.fromGo(3))
    }

    @Test
    fun everyStageHasAStringAndNoTwoStagesShareOne() {
        val ids = Stage.values().map { stage -> stage to stageDetail(Progress(stage)) }
        val seen = HashMap<Int, Stage>()
        for ((stage, id) in ids) {
            assertTrue("$stage has no string", id != 0)
            val clash = seen.put(id, stage)
            assertEquals("$stage and $clash say the same thing", null, clash)
        }
    }

    @Test
    fun theRouteChangesTheWordsOnlyWhereItMatters() {
        val unknown = stageDetail(Progress(Stage.WAITING_FOR_REQUEST))
        val relay = stageDetail(Progress(Stage.WAITING_FOR_REQUEST, route = Route.RELAY))
        val local = stageDetail(Progress(Stage.WAITING_FOR_REQUEST, route = Route.LOCAL))
        assertEquals(3, setOf(unknown, relay, local).size)
        // A direct address is not worth a variant: it reads as plain "connected".
        assertEquals(unknown, stageDetail(Progress(Stage.WAITING_FOR_REQUEST, route = Route.DIRECT)))
        // Where the route is not yet known, or not worth saying, it makes no difference.
        assertEquals(
            stageDetail(Progress(Stage.LOOKING_UP)),
            stageDetail(Progress(Stage.LOOKING_UP, route = Route.RELAY)),
        )
        assertNotEquals(unknown, stageDetail(Progress(Stage.SENDING_ANSWER)))
    }
}
