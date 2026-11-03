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
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.READINESS_CHIP_TAG
import com.github.muelli.synctang.ui.ReadinessChip
import com.github.muelli.synctang.ui.SynctangTheme
import org.junit.Rule
import org.junit.Test

/**
 * The chip has words as well as a colour, announces politely, announces without the age (so
 * that it speaks when the state changes and not when only the age does), and takes no focus.
 * Written to be run on a device.
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
}
