// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.viewmodel.compose.viewModel

/**
 * The whole app: one screen that is either "pair with a machine" or
 * "unlock the machine you paired with".
 *
 * Deliberately not ported from the reference app this project borrowed
 * its transport from: the quick settings tile, the home screen widget,
 * and the build-a-second-APK-per-machine trick. The plan for this app
 * asks for one Unlock action, and every one of those features is
 * another entry point that has to be got right in front of a key that
 * unlocks a disk.
 */
class MainActivity : FragmentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val gate = BiometricGateImpl(this)
        setContent {
            MaterialTheme(colorScheme = darkColorScheme()) {
                Surface(modifier = Modifier.fillMaxSize()) {
                    SynctangApp(gate)
                }
            }
        }
    }
}

@Composable
fun SynctangApp(gate: BiometricGate, model: UnlockViewModel = viewModel()) {
    val state by model.state.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        when (val current = state) {
            is UnlockUiState.NeedsPairing -> PairingScreen(current.identity, model)
            is UnlockUiState.Ready -> ReadyScreen(current, model)
            is UnlockUiState.Connecting -> Busy(stringResource(R.string.unlock_connecting))
            is UnlockUiState.Working -> Busy(stringResource(R.string.unlock_working))
            is UnlockUiState.ConfirmCode -> ConfirmCodeScreen(current, model)
            is UnlockUiState.Approve -> ApproveScreen(current, gate, model)
            is UnlockUiState.Done -> DoneScreen(current, model)
            is UnlockUiState.Failed -> FailedScreen(current, model)
        }
    }
}

@Composable
private fun Busy(message: String) {
    CircularProgressIndicator()
    Text(message, style = MaterialTheme.typography.bodyLarge)
}

@Composable
private fun PairingScreen(identity: Identity, model: UnlockViewModel) {
    var typed by remember { mutableStateOf("") }
    var rejected by remember { mutableStateOf(false) }

    Text(stringResource(R.string.pair_title), style = MaterialTheme.typography.headlineSmall)
    Text(stringResource(R.string.pair_scan_hint), style = MaterialTheme.typography.bodyMedium)

    QrScanner(
        onScanned = { scanned -> rejected = !model.pair(scanned) },
        modifier = Modifier
            .fillMaxWidth()
            .height(260.dp),
    )

    OutlinedTextField(
        value = typed,
        onValueChange = {
            typed = it
            rejected = false
        },
        label = { Text(stringResource(R.string.pair_manual_label)) },
        singleLine = false,
        modifier = Modifier.fillMaxWidth(),
    )
    Button(
        onClick = { rejected = !model.pair(typed) },
        enabled = typed.isNotBlank(),
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

    Spacer(Modifier.height(8.dp))
    EnrolmentDetails(identity)
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
        "unlocker enrol --device /dev/... \\\n" +
            "  --pubkey ${identity.publicKeyHex} \\\n" +
            "  --transport-id ${identity.deviceId} \\\n" +
            "  --name phone"
    }

    Text(stringResource(R.string.pair_enrol_title), style = MaterialTheme.typography.titleMedium)
    Text(stringResource(R.string.pair_enrol_hint), style = MaterialTheme.typography.bodyMedium)
    SelectionContainer {
        Text(
            command,
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
        )
    }
    OutlinedButton(onClick = { context.copyToClipboard(command) }) {
        Text(stringResource(R.string.pair_copy))
    }
    Text(identity.backingDescription(), style = MaterialTheme.typography.bodySmall)
}

@Composable
private fun ReadyScreen(state: UnlockUiState.Ready, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.unlock_waiting_hint), style = MaterialTheme.typography.bodyMedium)
    Button(onClick = { model.connect() }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.unlock_action))
    }
    Spacer(Modifier.height(8.dp))
    EnrolmentDetails(state.identity)
    TextButton(onClick = { model.forgetMachine() }) {
        Text(stringResource(R.string.unlock_forget))
    }
}

@Composable
private fun ConfirmCodeScreen(state: UnlockUiState.ConfirmCode, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.confirm_title), style = MaterialTheme.typography.headlineSmall)
    Text(stringResource(R.string.confirm_hint), style = MaterialTheme.typography.bodyMedium)
    Text(state.code, style = MaterialTheme.typography.displaySmall, fontFamily = FontFamily.Monospace)
    Labelled(stringResource(R.string.unlock_request_identity), state.peerId)
    Button(onClick = { model.submitConfirmCode(state.code) }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.confirm_action))
    }
    OutlinedButton(onClick = { model.cancel() }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.unlock_decline))
    }
}

@Composable
private fun ApproveScreen(state: UnlockUiState.Approve, gate: BiometricGate, model: UnlockViewModel) {
    Text(stringResource(R.string.unlock_request_title), style = MaterialTheme.typography.headlineSmall)
    MachineHeading(state.machine)
    // The verified identity is shown as well as the name, because the
    // name is only what the machine called itself and nothing proves
    // it; the device ID is derived from the certificate the peer
    // proved possession of during the handshake.
    Labelled(stringResource(R.string.unlock_request_identity), state.peerId)
    Labelled(stringResource(R.string.unlock_request_key), state.kidHex)

    Button(onClick = { model.approve(gate) }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.unlock_approve))
    }
    OutlinedButton(onClick = { model.cancel() }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.unlock_decline))
    }
}

@Composable
private fun DoneScreen(state: UnlockUiState.Done, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.unlock_done), style = MaterialTheme.typography.bodyLarge)
    Button(onClick = { model.refresh() }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.unlock_again))
    }
}

@Composable
private fun FailedScreen(state: UnlockUiState.Failed, model: UnlockViewModel) {
    state.machine?.let { MachineHeading(it) }
    Text(stringResource(R.string.error_title), style = MaterialTheme.typography.headlineSmall)
    SelectionContainer {
        Text(
            state.message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
        )
    }
    Button(onClick = { model.refresh() }, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.error_retry))
    }
}

@Composable
private fun MachineHeading(machine: Machine) {
    Labelled(
        stringResource(R.string.unlock_machine),
        machine.name.ifBlank { machine.deviceId },
    )
}

@Composable
private fun Labelled(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        SelectionContainer {
            Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
        }
    }
}

@Composable
private fun Identity.backingDescription(): String = when (backing) {
    KeyHolderKey.Backing.STRONGBOX -> stringResource(R.string.identity_backing_strongbox)
    KeyHolderKey.Backing.TEE_OR_SOFTWARE -> stringResource(R.string.identity_backing_tee)
}

private fun Context.copyToClipboard(text: String) {
    val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    clipboard.setPrimaryClip(ClipData.newPlainText("synctang", text))
}
