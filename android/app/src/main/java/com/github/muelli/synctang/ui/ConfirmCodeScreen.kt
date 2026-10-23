// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun ConfirmCodeScreen(state: UnlockUiState.ConfirmCode, onConfirm: (String) -> Unit, onDecline: () -> Unit) {
    ScreenHeader(stringResource(R.string.confirm_title), stringResource(R.string.confirm_hint))
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = MaterialTheme.shapes.medium,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(
            state.code,
            style = MaterialTheme.typography.displayMedium,
            fontFamily = FontFamily.Monospace,
            textAlign = TextAlign.Center,
            modifier = Modifier
                .fillMaxWidth()
                .padding(Spacing.between),
        )
    }
    MachineIdentity(state.machine, state.peerId, onEscape = onDecline)
    Button(
        onClick = { onConfirm(state.code) },
        modifier = Modifier
            .heroAction()
            .focusRequester(rememberAutoFocusRequester())
            .onEscape(onDecline)
            .focusRing(),
    ) {
        Text(stringResource(R.string.confirm_action))
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
