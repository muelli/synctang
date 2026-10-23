// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.BiometricGate
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ApproveScreen(state: UnlockUiState.Approve, gate: BiometricGate, model: UnlockViewModel) {
    Text(stringResource(R.string.unlock_request_title), style = MaterialTheme.typography.headlineSmall)
    MachineHeading(state.machine)
    // The verified identity is shown as well as the name, because the
    // name is only what the machine called itself and nothing proves
    // it; the device ID is derived from the certificate the peer
    // proved possession of during the handshake.
    Labelled(stringResource(R.string.unlock_request_identity), state.peerId)
    Labelled(stringResource(R.string.unlock_request_key), state.kidHex)

    // Focus starts on Approve even though it is the consequential
    // button, because approving still has to get past BiometricPrompt:
    // a stray Enter cannot unlock anything on its own, and putting
    // focus on Decline instead would mean every deliberate approval
    // starts with a Tab.
    Button(
        onClick = { model.approve(gate) },
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .onEscape { model.cancel() }
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_approve))
    }
    OutlinedButton(
        onClick = { model.cancel() },
        modifier = Modifier
            .fillMaxWidth()
            .onEscape { model.cancel() }
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_decline))
    }
}
