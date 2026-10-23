// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ReadyScreen(state: UnlockUiState.Ready, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.unlock_waiting_hint), style = MaterialTheme.typography.bodyMedium)
    Button(
        onClick = { model.connect() },
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_action))
    }
    // Not destructive: the machine above stays paired, and stays the
    // one Unlock dials, until a new pairing has actually worked.
    TextButton(onClick = { model.startPairing() }, modifier = Modifier.focusRing()) {
        Text(stringResource(R.string.pair_again))
    }
    Spacer(Modifier.height(8.dp))
    // Collapsed, and worded for a phone that is already enrolled. Shown
    // open on this screen it read as an instruction still to be carried
    // out, which is how a successful QR pairing came to look like it had
    // produced a command to go and run.
    var showManual by rememberSaveable { mutableStateOf(false) }
    TextButton(onClick = { showManual = !showManual }, modifier = Modifier.focusRing()) {
        Text(
            stringResource(
                if (showManual) R.string.enrol_manual_hide else R.string.enrol_manual_show,
            ),
        )
    }
    if (showManual) {
        EnrolmentDetails(
            state.identity,
            title = stringResource(R.string.enrol_manual_title),
            hint = stringResource(R.string.enrol_manual_hint),
        )
    }
}
