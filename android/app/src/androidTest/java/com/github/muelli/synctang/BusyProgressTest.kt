// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.isFocusable
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.BUSY_ELAPSED_TAG
import com.github.muelli.synctang.ui.Busy
import com.github.muelli.synctang.ui.PAIR_CODE_TAG
import com.github.muelli.synctang.ui.SynctangTheme
import org.junit.Rule
import org.junit.Test

/**
 * The busy screen now says what it is doing. The line saying how far it has got is
 * announced politely when it changes; the elapsed time is not a live region, or a
 * screen reader would read out every second. Nothing new takes focus, and Cancel is
 * still the one control. Written to be run on a device; the JVM tests cover the
 * logic behind it.
 */
class BusyProgressTest {

    @get:Rule
    val rule = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val machine = Machine("MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ", "workstation")

    private fun show(progress: Progress, nowMillis: Long = 12_000) {
        // The elapsed counter ticks for as long as the screen is up, so the test clock
        // is stepped by hand rather than left to run on.
        rule.mainClock.autoAdvance = false
        rule.setContent {
            SynctangTheme {
                Busy("Connecting", machine, onCancel = {}, progress = progress, clock = { nowMillis })
            }
        }
        rule.mainClock.advanceTimeByFrame()
    }

    @Test
    fun theStageLineIsAPoliteLiveRegion() {
        show(Progress(Stage.LOOKING_UP, attempt = 1, startedAtMillis = 0))
        rule.onNodeWithText(context.getString(R.string.stage_looking_up))
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite))
    }

    @Test
    fun theElapsedCounterIsNotALiveRegion() {
        show(Progress(Stage.LOOKING_UP, attempt = 1, startedAtMillis = 0))
        rule.onNodeWithTag(BUSY_ELAPSED_TAG)
            .assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.LiveRegion))
            .assertTextEquals(context.getString(R.string.busy_elapsed, 12))
    }

    @Test
    fun theAttemptIsShownOnlyAfterTheFirst() {
        show(Progress(Stage.CONNECTING, attempt = 3, startedAtMillis = 0))
        rule.onNodeWithTag(BUSY_ELAPSED_TAG).assertTextEquals(
            context.getString(R.string.busy_elapsed, 12) + " · " + context.getString(R.string.busy_attempt, 3),
        )
    }

    @Test
    fun nothingNewCanTakeFocusAndCancelIsStillTheOneControl() {
        show(Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1, startedAtMillis = 0))
        rule.onAllNodes(hasClickAction()).assertCountEquals(1)
        rule.onAllNodes(isFocusable()).assertCountEquals(1)
        rule.onNodeWithText(context.getString(R.string.busy_cancel)).assert(hasClickAction())
    }

    // The code to compare with the machine's, while its operator decides.

    @Test
    fun aPairingShowsTheCodeAndSaysWhatToDoIfItDiffers() {
        show(Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1, startedAtMillis = 0, shortCode = "483 291"))
        rule.onNodeWithTag(PAIR_CODE_TAG).assertTextEquals("483 291").assertIsDisplayed()
        rule.onNodeWithText(context.getString(R.string.pair_code_intro)).assertIsDisplayed()
        rule.onNodeWithText(context.getString(R.string.pair_code_warning)).assertIsDisplayed()
    }

    @Test
    fun theCodeIsReadOutDigitByDigit() {
        show(Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1, startedAtMillis = 0, shortCode = "483 291"))
        rule.onNodeWithTag(PAIR_CODE_TAG)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("4, 8, 3, 2, 9, 1")))
    }

    @Test
    fun noCodeIsShownBeforeTheRequestHasBeenSent() {
        show(Progress(Stage.CONNECTING, attempt = 1, startedAtMillis = 0))
        rule.onNodeWithTag(PAIR_CODE_TAG).assertDoesNotExist()
    }

    @Test
    fun theCodeAddsNothingToFocusAndCancelIsStillTheOneControl() {
        show(Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1, startedAtMillis = 0, shortCode = "483 291"))
        rule.onAllNodes(hasClickAction()).assertCountEquals(1)
        rule.onAllNodes(isFocusable()).assertCountEquals(1)
    }
}
