// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assertIsNotFocused
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.pressKey
import androidx.compose.ui.test.requestFocus
import androidx.compose.ui.test.withKeyDown
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test

/**
 * Android's desktop mode puts this app in a window with a keyboard and
 * no expectation that anyone will reach out and touch the screen. The
 * unlock flow is short and mostly buttons, so it is nearly usable that
 * way by accident; the parts that are not are the ones worth pinning
 * down, because each is the difference between "press Enter" and "give
 * up and find the touchscreen".
 *
 * These exercise the modifiers rather than whole screens: the screens
 * are assembled from them, and a helper that is wrong is wrong on
 * every screen at once.
 */
@OptIn(ExperimentalTestApi::class)
class KeyboardNavigationTest {

    @get:Rule
    val rule = createComposeRule()

    /**
     * Leave Android's touch mode before every test.
     *
     * In touch mode the focus system is deliberately inert: there is
     * no focus and no focus ring, because on a touchscreen with no
     * keyboard there should not be. Every assertion in this class is
     * therefore false in touch mode and true out of it, and a device
     * enters touch mode the moment anything taps the screen. These
     * tests passed the first time only because an earlier adb key
     * injection had happened to leave this emulator out of touch mode,
     * which is exactly the kind of ambient state a test must not
     * depend on: on a freshly booted device they would all have
     * failed, for a reason that has nothing to do with the code.
     */
    @Before
    fun leaveTouchMode() {
        InstrumentationRegistry.getInstrumentation().setInTouchMode(false)
    }

    /**
     * A multi-line text field swallows Tab as a literal tab character,
     * which traps keyboard focus: the field is where a keyboard user
     * pastes a device ID, so trapping focus there strands them at
     * precisely the point they need to move on to the button.
     */
    @Test
    fun tabMovesFocusOutOfAMultiLineField() {
        rule.setContent {
            Column {
                var typed by remember { mutableStateOf("") }
                BasicTextField(
                    value = typed,
                    onValueChange = { typed = it },
                    singleLine = false,
                    modifier = Modifier.testTag("field").tabMovesFocus(),
                )
                Button(onClick = {}, modifier = Modifier.testTag("after")) { Text("after") }
            }
        }

        rule.onNodeWithTag("field").requestFocus()
        rule.onNodeWithTag("field").assertIsFocused()

        rule.onNodeWithTag("field").performKeyInput { pressKey(Key.Tab) }

        rule.onNodeWithTag("field").assertIsNotFocused()
        rule.onNodeWithTag("after").assertIsFocused()
    }

    /** Shift-Tab has to go back, or the field is a one-way door. */
    @Test
    fun shiftTabMovesFocusBackwards() {
        rule.setContent {
            Column {
                Button(onClick = {}, modifier = Modifier.testTag("before")) { Text("before") }
                var typed by remember { mutableStateOf("") }
                BasicTextField(
                    value = typed,
                    onValueChange = { typed = it },
                    singleLine = false,
                    modifier = Modifier.testTag("field").tabMovesFocus(),
                )
            }
        }

        rule.onNodeWithTag("field").requestFocus()
        rule.onNodeWithTag("field").performKeyInput {
            withKeyDown(Key.ShiftLeft) { pressKey(Key.Tab) }
        }

        rule.onNodeWithTag("before").assertIsFocused()
    }

    /**
     * Escape is how a desktop user backs out of something. On the
     * approval screens that means declining, which is the safe
     * direction: the machine is told no and stays locked.
     */
    @Test
    fun escapeTriggersTheHandler() {
        var escaped = 0
        rule.setContent {
            Button(
                onClick = {},
                modifier = Modifier.testTag("button").onEscape { escaped++ },
            ) { Text("button") }
        }

        rule.onNodeWithTag("button").requestFocus()
        rule.onNodeWithTag("button").performKeyInput { pressKey(Key.Escape) }

        rule.runOnIdle { assertEquals(1, escaped) }
    }

    /** Any other key must still reach whatever would normally handle it. */
    @Test
    fun onEscapeDoesNotSwallowOtherKeys() {
        var escaped = 0
        rule.setContent {
            Button(
                onClick = {},
                modifier = Modifier.testTag("button").onEscape { escaped++ },
            ) { Text("button") }
        }

        rule.onNodeWithTag("button").requestFocus()
        rule.onNodeWithTag("button").performKeyInput { pressKey(Key.A) }

        rule.runOnIdle { assertEquals(0, escaped) }
    }

    /**
     * Every screen should arrive with its primary action already
     * focused, so that a keyboard user can act without first hunting
     * for where focus went.
     */
    @Test
    fun autoFocusFocusesOnFirstComposition() {
        rule.setContent {
            val requester = rememberAutoFocusRequester()
            Column {
                Button(onClick = {}, modifier = Modifier.testTag("first")) { Text("first") }
                Button(
                    onClick = {},
                    modifier = Modifier.testTag("primary").focusRequester(requester),
                ) { Text("primary") }
            }
        }

        rule.onNodeWithTag("primary").assertIsFocused()
        rule.onNodeWithTag("first").assertIsNotFocused()
    }

    /**
     * The camera preview is a large, non-interactive rectangle. Left
     * focusable it becomes a stop on the way to the manual-entry field,
     * for a control a keyboard user cannot do anything with, on a
     * device that in desktop mode may well have no camera at all.
     */
    @Test
    fun theScannerIsSkippedByTabNavigation() {
        var scannerFocused = false
        rule.setContent {
            Column {
                Button(onClick = {}, modifier = Modifier.testTag("before")) { Text("before") }
                Column(
                    modifier = Modifier
                        .testTag("scanner")
                        .notKeyboardFocusable()
                        .onFocusChanged { if (it.isFocused) scannerFocused = true },
                ) { Text("preview") }
                Button(onClick = {}, modifier = Modifier.testTag("after")) { Text("after") }
            }
        }

        rule.onNodeWithTag("before").requestFocus()
        rule.onNodeWithTag("before").performKeyInput { pressKey(Key.Tab) }

        rule.runOnIdle { assertTrue("the scanner took keyboard focus", !scannerFocused) }
        rule.onNodeWithTag("after").assertIsFocused()
    }
}
