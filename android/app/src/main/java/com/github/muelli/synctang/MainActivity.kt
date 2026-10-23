// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import androidx.activity.compose.BackHandler
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
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
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
        // The gate is an interface, so test mode swaps it rather than
        // threading a flag through the unlock flow: there is then no
        // branch inside the flow that could behave differently in the
        // build people actually use.
        val gate: BiometricGate =
            if (TestMode.noBiometric) OpenGate else BiometricGateImpl(this)
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

    // Only intercepted where something is waiting on this phone; on
    // every other screen back leaves the app as usual.
    val back = backAction(state)
    BackHandler(enabled = back != BackAction.EXIT) {
        when (back) {
            BackAction.CANCEL -> model.cancel()
            BackAction.KEEP_CURRENT_MACHINE -> model.keepCurrentMachine()
            BackAction.IGNORE, BackAction.EXIT -> Unit
        }
    }

    val snackbar = remember { SnackbarHostState() }
    UndoReplace(state as? UnlockUiState.Ready, snackbar, model)

    Scaffold(
        snackbarHost = { UndoSnackbarHost(snackbar) },
        // The Surface around this already paints the background.
        containerColor = Color.Transparent,
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            when (val current = state) {
                is UnlockUiState.NeedsPairing -> PairingScreen(current, model)
                is UnlockUiState.Ready -> ReadyScreen(current, model)
                is UnlockUiState.Connecting -> Busy(stringResource(R.string.unlock_connecting), onCancel = model::cancel)
                is UnlockUiState.Pairing -> Busy(stringResource(R.string.pair_working), onCancel = model::cancel)
                is UnlockUiState.Working -> Busy(stringResource(R.string.unlock_working))
                is UnlockUiState.ConfirmCode -> ConfirmCodeScreen(current, model)
                is UnlockUiState.Approve -> ApproveScreen(current, gate, model)
                is UnlockUiState.Done -> DoneScreen(current, model)
                is UnlockUiState.Failed -> FailedScreen(current, model)
            }
        }
    }
}

/**
 * A pairing that replaced a different machine is offered back, the
 * way Android apps offer to undo anything done in one tap: a snackbar
 * with Undo. A pairing without a one-time code cannot fail, so this is
 * the only thing between a mistyped device ID and a working pairing
 * lost.
 */
@Composable
private fun UndoReplace(ready: UnlockUiState.Ready?, snackbar: SnackbarHostState, model: UnlockViewModel) {
    val replaced = ready?.replaced
    val message = replaced?.let {
        stringResource(R.string.pair_replaced, ready.machine.label(), it.label())
    }
    val undo = stringResource(R.string.pair_undo)
    LaunchedEffect(replaced) {
        if (message == null) return@LaunchedEffect
        val result = snackbar.showSnackbar(
            message,
            actionLabel = undo,
            withDismissAction = true,
            duration = SnackbarDuration.Long,
        )
        when (result) {
            SnackbarResult.ActionPerformed -> model.undoReplace()
            SnackbarResult.Dismissed -> model.dismissUndo()
        }
    }
}

@Composable
private fun UndoSnackbarHost(state: SnackbarHostState) {
    SnackbarHost(state) { data ->
        Snackbar(
            modifier = Modifier
                .padding(12.dp)
                .onEscape { data.dismiss() },
            action = {
                TextButton(onClick = { data.performAction() }, modifier = Modifier.focusRing()) {
                    Text(data.visuals.actionLabel.orEmpty())
                }
            },
            dismissAction = {
                TextButton(onClick = { data.dismiss() }, modifier = Modifier.focusRing()) {
                    Text(stringResource(R.string.pair_undo_dismiss))
                }
            },
        ) {
            Text(data.visuals.message)
        }
    }
}

private fun Machine.label(): String = name.ifBlank { deviceId }

/**
 * A spinner, and a Cancel button when [onCancel] is given. A dial can
 * run for a minute and a half against a machine that is not waiting,
 * which is too long to offer nothing but the back gesture.
 */
@Composable
private fun Busy(message: String, onCancel: (() -> Unit)? = null) {
    CircularProgressIndicator()
    Text(message, style = MaterialTheme.typography.bodyLarge)
    if (onCancel != null) {
        OutlinedButton(
            onClick = onCancel,
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(rememberAutoFocusRequester())
                .onEscape(onCancel)
                .focusRing(),
        ) {
            Text(stringResource(R.string.busy_cancel))
        }
    }
}

@Composable
private fun PairingScreen(state: UnlockUiState.NeedsPairing, model: UnlockViewModel) {
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
private fun EnrolmentDetails(
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

@Composable
private fun ReadyScreen(state: UnlockUiState.Ready, model: UnlockViewModel) {
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

@Composable
private fun ConfirmCodeScreen(state: UnlockUiState.ConfirmCode, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.confirm_title), style = MaterialTheme.typography.headlineSmall)
    Text(stringResource(R.string.confirm_hint), style = MaterialTheme.typography.bodyMedium)
    Text(state.code, style = MaterialTheme.typography.displaySmall, fontFamily = FontFamily.Monospace)
    Labelled(stringResource(R.string.unlock_request_identity), state.peerId)
    Button(
        onClick = { model.submitConfirmCode(state.code) },
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .onEscape { model.cancel() }
            .focusRing(),
    ) {
        Text(stringResource(R.string.confirm_action))
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

@Composable
private fun DoneScreen(state: UnlockUiState.Done, model: UnlockViewModel) {
    MachineHeading(state.machine)
    Text(stringResource(R.string.unlock_done), style = MaterialTheme.typography.bodyLarge)
    Button(
        onClick = { model.refresh() },
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
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
    // The machine's own words, or the raw error behind a failure the
    // app has no words for: of no use to most people, but the one thing
    // worth quoting in a bug report.
    state.detail?.let { detail ->
        SelectionContainer {
            Text(detail, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
        }
    }
    // A pairing code is single-use, so retrying a failed pairing means
    // a fresh scan, not a dial against a machine that never enrolled
    // this phone.
    val scanAgain = state.retry == Retry.SCAN_AGAIN
    Button(
        onClick = { if (scanAgain) model.startPairing() else model.refresh() },
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Text(stringResource(if (scanAgain) R.string.error_scan_again else R.string.error_retry))
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
