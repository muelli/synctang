// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.view.HapticFeedbackConstants
import android.view.View
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalView

/**
 * What the phone does to be felt: a short buzz when a machine becomes ready, and another when an
 * unlock is done. Both are only a nudge for someone who is not looking at the screen; each screen
 * says the same in words, so nothing depends on feeling them.
 */
interface Buzzer {
    fun machineReady()
    fun unlocked()
}

/**
 * The system's haptic feedback for the view the screen is in. It needs no permission, and it does
 * nothing when the person has turned touch feedback off in the system settings, which is the
 * setting that decides whether an app may buzz.
 */
private class ViewBuzzer(private val view: View) : Buzzer {
    override fun machineReady() {
        view.performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK)
    }

    override fun unlocked() {
        view.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
    }
}

/** Replaces the view's own buzzer, so that a test can count the buzzes. */
val LocalBuzzer = staticCompositionLocalOf<Buzzer?> { null }

@Composable
internal fun rememberBuzzer(): Buzzer {
    val view = LocalView.current
    return LocalBuzzer.current ?: remember(view) { ViewBuzzer(view) }
}

/**
 * Whether going from [before] to [now] is a machine becoming ready: it was seen not to be, and now
 * it is. The first answer after a screen comes up ([before] null, or still [ReadinessState.CHECKING])
 * is not a change the person was waiting for, they are looking at it; nor is staying ready.
 */
fun buzzForReadiness(before: ReadinessState?, now: ReadinessState): Boolean =
    now == ReadinessState.WAITING &&
        before != null &&
        before != ReadinessState.CHECKING &&
        before != ReadinessState.WAITING
