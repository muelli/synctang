// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createComposeRule
import com.github.muelli.synctang.ui.DoneScreen
import com.github.muelli.synctang.ui.ReadinessChip
import com.github.muelli.synctang.ui.SynctangTheme
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test

/**
 * The phone buzzes when a machine becomes ready and when an unlock is done, through [Buzzer], so that
 * this can count the buzzes without a vibration motor. Written to be run on a device.
 */
class BuzzTest {

    @get:Rule
    val rule = createComposeRule()

    private class CountingBuzzer : Buzzer {
        var ready = 0
        var unlocked = 0
        override fun machineReady() { ready++ }
        override fun unlocked() { unlocked++ }
    }

    private val machine = Machine("DEVICE-ID", "the-machine")

    @Test
    fun theChipBuzzesWhenTheMachineBecomesReadyAndNotForTheFirstAnswerOrForStayingReady() {
        val buzzer = CountingBuzzer()
        var readiness by mutableStateOf(Readiness(ReadinessState.CHECKING))
        rule.setContent {
            CompositionLocalProvider(LocalBuzzer provides buzzer) { SynctangTheme { ReadinessChip(readiness) } }
        }
        readiness = Readiness(ReadinessState.WAITING, 5)
        rule.waitForIdle()
        assertEquals("the first answer is not a change", 0, buzzer.ready)

        readiness = Readiness(ReadinessState.WAITING, 20)
        rule.waitForIdle()
        assertEquals("the age moving is not a change", 0, buzzer.ready)

        readiness = Readiness(ReadinessState.STALE, 600)
        rule.waitForIdle()
        assertEquals("going away does not buzz", 0, buzzer.ready)

        readiness = Readiness(ReadinessState.WAITING, 3)
        rule.waitForIdle()
        assertEquals("becoming ready after not being so buzzes", 1, buzzer.ready)
        assertEquals(0, buzzer.unlocked)
    }

    @Test
    fun aChipThatComesUpReadyDoesNotBuzz() {
        val buzzer = CountingBuzzer()
        rule.setContent {
            CompositionLocalProvider(LocalBuzzer provides buzzer) { SynctangTheme { ReadinessChip(Readiness(ReadinessState.WAITING, 5)) } }
        }
        rule.waitForIdle()
        assertEquals(0, buzzer.ready)
    }

    @Test
    fun theDoneScreenBuzzesOnceWhenItComesUp() {
        val buzzer = CountingBuzzer()
        rule.setContent {
            CompositionLocalProvider(LocalBuzzer provides buzzer) { SynctangTheme { DoneScreen(UnlockUiState.Done(machine), {}) } }
        }
        rule.waitForIdle()
        assertEquals(1, buzzer.unlocked)
        assertEquals(0, buzzer.ready)
    }
}
