// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.QrScanner
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.enrolCommand
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester
import com.github.muelli.synctang.tabMovesFocus

@Composable
internal fun PairingScreen(state: UnlockUiState.NeedsPairing, model: UnlockViewModel) {
    val identity = state.identity
    var typed by remember { mutableStateOf("") }
    var rejected by remember { mutableStateOf(false) }

    Text(stringResource(R.string.pair_title), style = MaterialTheme.typography.headlineSmall)
    Text(stringResource(R.string.pair_scan_hint), style = MaterialTheme.typography.bodyMedium)
    state.previous?.let { previous ->
        val name = previous.name.ifBlank { previous.deviceId }
        Text(stringResource(R.string.pair_keeps_previous, name), style = MaterialTheme.typography.bodyMedium)
    }

    QrScanner(
        onScanned = { scanned -> model.pair(scanned).also { rejected = !it } },
        modifier = Modifier
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
        keyboardActions = KeyboardActions(onDone = { rejected = !model.pair(typed) }),
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(field)
            .tabMovesFocus()
            .focusRing(),
    )
    Button(
        onClick = { rejected = !model.pair(typed) },
        enabled = typed.isNotBlank(),
        modifier = Modifier.focusRing(),
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
            onClick = { model.keepCurrentMachine() },
            modifier = Modifier.focusRing(),
        ) {
            Text(stringResource(R.string.pair_keep_current, previous.name.ifBlank { previous.deviceId }))
        }
    }

    Spacer(Modifier.height(8.dp))
    EnrolmentDetails(identity)
}

/**
 * What the machine needs to be told, once, so that this phone can
 * unlock it. Shown as the exact command to run rather than as two
 * loose hex strings, because that is the form it is actually used in.
 */
@Composable
internal fun EnrolmentDetails(
    identity: Identity,
    title: String = stringResource(R.string.pair_enrol_title),
    hint: String = stringResource(R.string.pair_enrol_hint),
) {
    val context = LocalContext.current
    val command = remember(identity) {
        enrolCommand(identity.publicKeyHex, identity.deviceId)
    }

    Text(title, style = MaterialTheme.typography.titleMedium)
    Text(hint, style = MaterialTheme.typography.bodyMedium)
    SelectionContainer {
        Text(
            command,
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
        )
    }
    OutlinedButton(
        onClick = { context.copyToClipboard(command) },
        modifier = Modifier.focusRing(),
    ) {
        Text(stringResource(R.string.pair_copy))
    }
    Text(identity.backingDescription(), style = MaterialTheme.typography.bodySmall)
}
