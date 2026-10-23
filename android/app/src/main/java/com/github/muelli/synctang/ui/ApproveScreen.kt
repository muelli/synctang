// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ApproveScreen(state: UnlockUiState.Approve, onApprove: () -> Unit, onDecline: () -> Unit) {
    ScreenHeader(stringResource(R.string.unlock_request_title), stringResource(R.string.approve_explanation))
    MachineIdentity(state.machine, state.peerId, onEscape = onDecline)
    Identifier(stringResource(R.string.unlock_request_key), state.kidHex, shortKeyId(state.kidHex), onDecline)

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
            .onEscape(onDecline)
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_approve), style = MaterialTheme.typography.titleMedium)
    }
    OutlinedButton(
        onClick = onDecline,
        modifier = Modifier
            .fillMaxWidth()
            .onEscape(onDecline)
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_decline))
    }
}
