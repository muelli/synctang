// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ReadyScreen(state: UnlockUiState.Ready, onUnlock: () -> Unit, onPairAgain: () -> Unit) {
    // The machine is the headline: on this screen it is the only thing
    // there is to act on.
    Column {
        Text(stringResource(R.string.ready_paired_with), style = MaterialTheme.typography.labelLarge)
        Text(state.machine.label(), style = MaterialTheme.typography.headlineMedium)
        if (state.machine.name.isNotBlank()) {
            Text(
                shortDeviceId(state.machine.deviceId),
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Text(
        stringResource(R.string.unlock_waiting_hint),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Button(
        onClick = onUnlock,
        modifier = Modifier
            .heroAction()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Icon(Icons.Filled.Lock, contentDescription = null)
        Spacer(Modifier.width(Spacing.small))
        Text(stringResource(R.string.unlock_action), style = MaterialTheme.typography.titleMedium)
    }
    // Not destructive: the machine above stays paired, and stays the
    // one Unlock dials, until a new pairing has actually worked.
    TextButton(onClick = onPairAgain, modifier = Modifier.focusRing()) {
        Text(stringResource(R.string.pair_again))
    }
}
