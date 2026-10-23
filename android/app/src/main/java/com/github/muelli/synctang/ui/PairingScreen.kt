// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.QrScanner
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester
import com.github.muelli.synctang.tabMovesFocus

@Composable
internal fun PairingScreen(
    state: UnlockUiState.NeedsPairing,
    onPair: (String) -> Boolean,
    onKeepCurrent: () -> Unit,
    onEnrolByHand: () -> Unit,
    // A slot so that previews, which have no camera, can show the
    // permission prompts instead.
    scanner: @Composable (onScanned: (String) -> Boolean, modifier: Modifier) -> Unit =
        { onScanned, modifier -> QrScanner(onScanned, modifier) },
) {
    var typed by remember { mutableStateOf("") }
    var rejected by remember { mutableStateOf(false) }

    ScreenHeader(stringResource(R.string.pair_title), stringResource(R.string.pair_scan_hint))
    state.previous?.let { previous ->
        val name = previous.name.ifBlank { previous.deviceId }
        Text(stringResource(R.string.pair_keeps_previous, name), style = MaterialTheme.typography.bodyMedium)
    }

    scanner(
        { scanned -> onPair(scanned).also { rejected = !it } },
        Modifier
            .fillMaxWidth()
            .height(260.dp),
    )

    // The manual field, not the scanner, is the keyboard path onto this
    // screen, so it is what focus starts on.
    val field = rememberAutoFocusRequester()
    OutlinedTextField(
        value = typed,
        onValueChange = {
            typed = it
            rejected = false
        },
        label = { Text(stringResource(R.string.pair_manual_label)) },
        singleLine = false,
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { rejected = !onPair(typed) }),
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(field)
            .tabMovesFocus()
            .focusRing(),
    )
    Button(
        onClick = { rejected = !onPair(typed) },
        enabled = typed.isNotBlank(),
        modifier = Modifier
            .fillMaxWidth()
            .focusRing(),
    ) {
        Text(stringResource(R.string.pair_manual_action))
    }
    if (rejected) {
        Text(
            stringResource(R.string.pair_invalid),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
        )
    }
    state.previous?.let { previous ->
        OutlinedButton(
            onClick = onKeepCurrent,
            modifier = Modifier
                .fillMaxWidth()
                .focusRing(),
        ) {
            Text(stringResource(R.string.pair_keep_current, previous.name.ifBlank { previous.deviceId }))
        }
    }

    // For a machine that cannot show a pairing code: the command lives
    // with the phone's other details rather than on every first run.
    TextButton(onClick = onEnrolByHand, modifier = Modifier.focusRing()) {
        Text(stringResource(R.string.pair_by_hand))
    }
}
