// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.unit.dp

/**
 * Keyboard support, for Android's desktop mode and for anything else
 * with a physical keyboard attached.
 *
 * The app is a handful of screens with one obvious action each, so the
 * goal is not a shortcut for everything: it is that somebody who never
 * touches the screen is never stuck. That comes down to four things,
 * each of which is a way to get stuck if it is missing. Focus has to
 * start somewhere useful, Tab has to keep moving (including out of a
 * text field), Escape has to back out, and the thing that currently has
 * focus has to be visible.
 *
 * Compose already handles the easy half: a focused Button is activated
 * by Enter, NumPadEnter or Space, and a scrollable column scrolls with
 * the arrow keys and Page Up/Down. None of that needs code here.
 */

/**
 * A [FocusRequester] that takes focus once, when the composable it is
 * attached to first appears.
 *
 * Every screen arrives with its primary action focused. Without it,
 * focus starts nowhere: the first Tab press goes to whatever the focus
 * system considers first, which on these screens is a heading or a
 * camera preview rather than the button the screen exists for.
 *
 * [key] re-runs the request when it changes, so a screen that swaps its
 * content without leaving the composition focuses the new primary
 * action rather than leaving focus on something that has gone.
 */
@Composable
fun rememberAutoFocusRequester(key: Any? = Unit): FocusRequester {
    val requester = remember { FocusRequester() }
    LaunchedEffect(key) {
        // Wait for a frame before asking, and keep asking for a few.
        //
        // A FocusRequester is only usable once the modifier node it is
        // attached to has been through layout, which has not happened
        // when this effect first runs: requestFocus throws
        // "FocusRequester is not initialized" and, if that is quietly
        // swallowed, the screen simply arrives with nothing focused.
        // That is how the first version of this behaved, and it looked
        // exactly like a focus ring that had not been implemented.
        //
        // Retrying rather than waiting a fixed time because the number
        // of frames is not something to guess at; giving up after a
        // few because a requester that is never attached (a screen that
        // composes it conditionally) must not spin forever.
        repeat(ATTACH_ATTEMPTS) {
            withFrameNanos { }
            if (runCatching { requester.requestFocus() }.isSuccess) {
                return@LaunchedEffect
            }
        }
    }
    return requester
}

private const val ATTACH_ATTEMPTS = 5

/**
 * Makes Tab and Shift-Tab move focus rather than reach the field
 * underneath.
 *
 * A multi-line text field treats Tab as text and inserts it, which
 * traps keyboard focus in the field. On the pairing screen that field
 * is where a keyboard user pastes a device ID, so trapping focus there
 * strands them at exactly the moment they need to move on to the
 * button next to it. Single-line fields do not have the problem, but
 * this field is multi-line on purpose: a device ID is long and wrapping
 * beats scrolling sideways.
 */
@Composable
fun Modifier.tabMovesFocus(): Modifier {
    val focusManager = LocalFocusManager.current
    return onPreviewKeyEvent { event ->
        if (event.type == KeyEventType.KeyDown && event.key == Key.Tab) {
            focusManager.moveFocus(
                if (event.isShiftPressed) FocusDirection.Previous else FocusDirection.Next,
            )
            true
        } else {
            false
        }
    }
}

/**
 * Runs [onEscape] when Escape is pressed while this element, or
 * anything inside it, has focus.
 *
 * Used on the two screens that ask a question, where backing out means
 * declining. That is the safe direction by construction: the machine is
 * told no and stays locked, which is also what happens if the person
 * simply walks away.
 */
fun Modifier.onEscape(onEscape: () -> Unit): Modifier = onPreviewKeyEvent { event ->
    if (event.type == KeyEventType.KeyDown && event.key == Key.Escape) {
        onEscape()
        true
    } else {
        false
    }
}

/**
 * Draws a visible ring around this element while it has focus.
 *
 * Material's own focus indication is faint, and was designed on the
 * assumption that focus is a secondary way to drive a touchscreen. In
 * desktop mode it is the only way, and an indicator that has to be
 * hunted for is the same problem as no indicator. Deliberately uses the
 * theme's primary colour rather than a hand-picked one, so it stays
 * legible if the colour scheme changes.
 */
@Composable
fun Modifier.focusRing(cornerRadius: Int = 24): Modifier {
    var focused by remember { mutableStateOf(false) }
    val colour = if (focused) MaterialTheme.colorScheme.onSurface else Color.Transparent
    return this
        .onFocusChanged { focused = it.isFocused }
        // The ring is drawn at the outer edge and the control inset
        // inside it, so that it lands on the background rather than on
        // the control. A ring drawn on top of a filled button is the
        // primary colour on the primary colour: present, correct, and
        // invisible, which is how the first version of this shipped
        // past a screenshot. The inset is reserved whether or not the
        // ring is currently drawn, so focus does not shift the layout.
        .border(2.dp, colour, RoundedCornerShape(cornerRadius.dp))
        .padding(4.dp)
}

/**
 * Takes this element out of keyboard focus order entirely.
 *
 * For the camera preview: a large rectangle a keyboard user can do
 * nothing with, on a device that in desktop mode may not even have a
 * camera. Leaving it focusable makes it a stop on the way to the
 * manual-entry field that is the actual keyboard path.
 */
fun Modifier.notKeyboardFocusable(): Modifier = focusProperties { canFocus = false }
