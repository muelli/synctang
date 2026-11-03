// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import mobile.Mobile
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The app's Stage and Route numbers are those of the Go side. The JVM tests pin them
 * against a copy of the list; this checks them against the generated binding itself.
 */
class ProgressConstantsTest {

    @Test
    fun theStageNumbersAreThoseOfTheGoSide() {
        assertEquals(Mobile.StageNone, Stage.NONE.goNumber)
        assertEquals(Mobile.StageStarting, Stage.STARTING.goNumber)
        assertEquals(Mobile.StageLookingUp, Stage.LOOKING_UP.goNumber)
        assertEquals(Mobile.StageConnecting, Stage.CONNECTING.goNumber)
        assertEquals(Mobile.StageCheckingIdentity, Stage.CHECKING_IDENTITY.goNumber)
        assertEquals(Mobile.StageWaitingForRequest, Stage.WAITING_FOR_REQUEST.goNumber)
        assertEquals(Mobile.StageRequestReady, Stage.REQUEST_READY.goNumber)
        assertEquals(Mobile.StageSendingAnswer, Stage.SENDING_ANSWER.goNumber)
        assertEquals(Mobile.StageWaitingForVerdict, Stage.WAITING_FOR_VERDICT.goNumber)
        assertEquals(Mobile.StageUnlocked, Stage.UNLOCKED.goNumber)
        assertEquals(Mobile.StageSendingProof, Stage.SENDING_PROOF.goNumber)
        assertEquals(Mobile.StageWaitingForOperator, Stage.WAITING_FOR_OPERATOR.goNumber)
        assertEquals(Mobile.StageEnrolled, Stage.ENROLLED.goNumber)
    }

    @Test
    fun theRouteNumbersAreThoseOfTheGoSide() {
        assertEquals(Mobile.RouteUnknown, Route.UNKNOWN.goNumber)
        assertEquals(Mobile.RouteRelay, Route.RELAY.goNumber)
        assertEquals(Mobile.RouteLocal, Route.LOCAL.goNumber)
        assertEquals(Mobile.RouteDirect, Route.DIRECT.goNumber)
    }
}
