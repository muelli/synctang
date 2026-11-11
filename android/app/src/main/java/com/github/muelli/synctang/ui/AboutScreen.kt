// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.enrolCommand
import com.github.muelli.synctang.focusRing

/**
 * The phone's identity, when About this phone may be opened over
 * [state]: only where nothing is under way, so that it can never hide a
 * machine waiting for an answer.
 */
fun aboutIdentity(state: UnlockUiState): Identity? = when (state) {
    is UnlockUiState.NeedsPairing -> state.identity
    is UnlockUiState.Ready -> state.identity
    is UnlockUiState.Machines -> state.identity
    else -> null
}

/**
 * What only someone setting things up by hand needs: this phone's own
 * identifiers in full, how its key is held, and the enrolment command
 * for a machine that cannot show a pairing code.
 */
@Composable
internal fun AboutScreen(identity: Identity, version: String) {
    SectionTitle(stringResource(R.string.about_phone_title))
    Labelled(stringResource(R.string.identity_device_id), identity.deviceId)
    Labelled(stringResource(R.string.identity_public_key), identity.publicKeyHex)
    Text(identity.backingDescription(), style = MaterialTheme.typography.bodySmall)

    EnrolmentDetails(identity)

    Text(stringResource(R.string.about_version, version), style = MaterialTheme.typography.bodySmall)
}

/**
 * What the machine needs to be told, once, so that this phone can
 * unlock it. Shown as the exact command to run rather than as two
 * loose hex strings, because that is the form it is actually used in.
 */
@Composable
private fun EnrolmentDetails(identity: Identity) {
    val context = LocalContext.current
    val command = remember(identity) {
        enrolCommand(identity.publicKeyHex, identity.deviceId)
    }

    SectionTitle(stringResource(R.string.enrol_manual_title))
    Text(stringResource(R.string.enrol_manual_hint), style = MaterialTheme.typography.bodyMedium)
    SelectionContainer {
        Text(
            command,
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
        )
    }
    var copied by remember(command) { mutableStateOf(false) }
    val copy = {
        context.copyToClipboard(command)
        copied = true
    }
    OutlinedButton(
        onClick = copy,
        modifier = Modifier
            .fillMaxWidth()
            .shortcut(AppAction.COPY_COMMAND, onInvoke = copy)
            .focusRing(),
    ) {
        Text(stringResource(R.string.pair_copy))
    }
    // Android 13 and later say so themselves; earlier versions, and a
    // screen reader, would otherwise get no sign that anything happened.
    if (copied) {
        Text(
            stringResource(R.string.pair_copied),
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        )
    }
}

@Composable
internal fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
}
