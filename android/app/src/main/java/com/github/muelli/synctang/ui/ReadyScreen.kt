// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ReadyScreen(state: UnlockUiState.Ready, onUnlock: () -> Unit, onPairAgain: () -> Unit) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.unlock_waiting_hint), style = MaterialTheme.typography.bodyMedium)
    Button(
        onClick = onUnlock,
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_action))
    }
    // Not destructive: the machine above stays paired, and stays the
    // one Unlock dials, until a new pairing has actually worked.
    TextButton(onClick = onPairAgain, modifier = Modifier.focusRing()) {
        Text(stringResource(R.string.pair_again))
    }
}
