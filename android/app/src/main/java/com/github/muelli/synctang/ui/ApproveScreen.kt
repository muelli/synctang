// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.approveTimeLeft
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester
import kotlinx.coroutines.delay

@Composable
internal fun ApproveScreen(
    state: UnlockUiState.Approve,
    onApprove: () -> Unit,
    onDecline: () -> Unit,
    clock: () -> Long = System::currentTimeMillis,
) {
    ScreenHeader(stringResource(R.string.unlock_request_title), stringResource(R.string.approve_explanation))
    MachineIdentity(state.machine, state.peerId)
    Identifier(
        stringResource(R.string.unlock_request_key),
        state.kidHex,
        shortKeyId(state.kidHex),
        AppAction.TOGGLE_KEY_ID,
    )
    if (state.expiresAtMillis > 0) TimeLeftText(state.expiresAtMillis, clock)

    // Focus starts on Approve even though it is the consequential
    // button, because approving still has to get past BiometricPrompt:
    // a stray Enter cannot unlock anything on its own, and putting
    // focus on Decline instead would mean every deliberate approval
    // starts with a Tab.
    Button(
        onClick = onApprove,
        modifier = Modifier
            .heroAction()
            .focusRequester(rememberAutoFocusRequester())
            .shortcut(AppAction.APPROVE, onInvoke = onApprove)
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_approve), style = MaterialTheme.typography.titleMedium)
    }
    OutlinedButton(
        onClick = onDecline,
        modifier = Modifier
            .fillMaxWidth()
            .shortcut(AppAction.DECLINE, onInvoke = onDecline)
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_decline))
    }
}

/**
 * How long is left to answer, in plain text that changes about every five seconds,
 * and, once, a warning when it is running out. The figure is not a live region: a
 * counter announced every tick would be read out over and over. The warning is, and
 * is announced politely when it appears, which is the one moment that matters.
 * Neither takes focus.
 */
@Composable
private fun TimeLeftText(expiresAtMillis: Long, clock: () -> Long) {
    var left by remember(expiresAtMillis) { mutableStateOf(approveTimeLeft(expiresAtMillis, clock())) }
    LaunchedEffect(expiresAtMillis) {
        while (true) {
            delay(1_000)
            val next = approveTimeLeft(expiresAtMillis, clock())
            if (next != left) left = next
        }
    }
    Text(
        stringResource(R.string.approve_time_left, left.seconds),
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.testTag(APPROVE_TIME_LEFT_TAG),
    )
    if (left.runningOut) {
        Text(
            stringResource(R.string.approve_running_out),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        )
    }
}

/** Marks the time-left line, so a test can find it without knowing its words. */
internal const val APPROVE_TIME_LEFT_TAG = "approve-time-left"
