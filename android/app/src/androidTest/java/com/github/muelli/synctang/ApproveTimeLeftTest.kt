// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.APPROVE_TIME_LEFT_TAG
import com.github.muelli.synctang.ui.ApproveScreen
import com.github.muelli.synctang.ui.SynctangTheme
import org.junit.Rule
import org.junit.Test

/**
 * The Approve screen shows how long is left, as plain text that is not a live region
 * (so it is not read out every few seconds), and warns once, politely, when it is
 * running out. Written to be run on a device; the arithmetic is in ApproveWindowTest.
 */
class ApproveTimeLeftTest {

    @get:Rule
    val rule = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val machine = Machine("MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ", "workstation")

    private fun show(expiresAt: Long, now: Long) {
        // The text refreshes on a timer for as long as the screen is up, so the test clock
        // is stepped by hand rather than left to run on.
        rule.mainClock.autoAdvance = false
        rule.setContent {
            SynctangTheme {
                ApproveScreen(
                    UnlockUiState.Approve(machine, "PEER", "abcd", expiresAtMillis = expiresAt),
                    onApprove = {},
                    onDecline = {},
                    clock = { now },
                )
            }
        }
        rule.mainClock.advanceTimeByFrame()
    }

    @Test
    fun theTimeLeftIsPlainTextAndNotALiveRegion() {
        show(expiresAt = 75_000, now = 0)
        rule.onNodeWithTag(APPROVE_TIME_LEFT_TAG)
            .assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.LiveRegion))
            .assertTextEquals(context.getString(R.string.approve_time_left, 75))
        rule.onNodeWithText(context.getString(R.string.approve_running_out)).assertDoesNotExist()
    }

    @Test
    fun runningOutIsAnnouncedPolitely() {
        show(expiresAt = 75_000, now = 62_000)
        rule.onNodeWithText(context.getString(R.string.approve_running_out))
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite))
    }
}
