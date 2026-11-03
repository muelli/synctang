// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.isFocusable
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.click
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.performTouchInput
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.READINESS_CHIP_TAG
import com.github.muelli.synctang.ui.ReadinessChip
import com.github.muelli.synctang.ui.SynctangTheme
import org.junit.Rule
import org.junit.Test

/**
 * The chip has words as well as a colour, announces politely, announces without the age (so
 * that it speaks when the state changes and not when only the age does), and takes no focus.
 * A long press on it asks for a check at once, for someone who does not want to wait for the
 * next poll; the Check again button is the way for everyone else. Written to be run on a
 * device.
 */
class ReadinessChipTest {

    @get:Rule
    val rule = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    @Test
    fun theWordsAreShownAndTheStateIsAPoliteLiveRegionWithoutTheAge() {
        rule.setContent { SynctangTheme { ReadinessChip(Readiness(ReadinessState.STALE, 47 * 60)) } }
        val shown = context.getString(R.string.readiness_stale_minutes, 47)
        rule.onNodeWithText(shown, useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.LiveRegion, LiveRegionMode.Polite))
            .assert(
                SemanticsMatcher.expectValue(
                    SemanticsProperties.ContentDescription,
                    listOf(context.getString(R.string.readiness_stale_announce)),
                ),
            )
    }

    @Test
    fun theChipTakesNoFocusAndOffersNoAction() {
        rule.setContent { SynctangTheme { ReadinessChip(Readiness(ReadinessState.WAITING, 5)) } }
        rule.onNodeWithTag(READINESS_CHIP_TAG).assert(SemanticsMatcher("is not focusable") { !isFocusable().matches(it) })
        rule.onNodeWithTag(READINESS_CHIP_TAG).assert(SemanticsMatcher("has no click action") { !hasClickAction().matches(it) })
    }

    @Test
    fun aLongPressAsksForACheckAtOnce() {
        val asked = AtomicInteger()
        rule.setContent { SynctangTheme { ReadinessChip(Readiness(ReadinessState.WAITING, 5), onCheckNow = { asked.incrementAndGet() }) } }
        rule.onNodeWithTag(READINESS_CHIP_TAG).performTouchInput { longClick() }
        rule.waitForIdle()
        assertEquals(1, asked.get())
    }

    @Test
    fun anOrdinaryTapDoesNotAsk() {
        val asked = AtomicInteger()
        rule.setContent { SynctangTheme { ReadinessChip(Readiness(ReadinessState.WAITING, 5), onCheckNow = { asked.incrementAndGet() }) } }
        rule.onNodeWithTag(READINESS_CHIP_TAG).performTouchInput { click() }
        rule.waitForIdle()
        assertEquals(0, asked.get())
    }
}
