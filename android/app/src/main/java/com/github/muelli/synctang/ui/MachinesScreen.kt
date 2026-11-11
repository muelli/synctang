// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.R
import com.github.muelli.synctang.Readiness
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

/**
 * Two machines or more: one row for each, the one unlocked last first. A row is what the Ready screen is
 * for one machine: its name, what discovery last said of it, and its own Unlock. The first row's Unlock has
 * the key and the focus; the others are reached by touch or by moving focus. Forgetting asks first.
 */
@Composable
internal fun MachinesScreen(
    state: UnlockUiState.Machines,
    onUnlock: (Machine) -> Unit,
    onCheckAgain: (Machine) -> Unit,
    onForget: (Machine) -> Unit,
    onPairAnother: () -> Unit,
) {
    ScreenHeader(stringResource(R.string.machines_title), stringResource(R.string.machines_hint))
    state.machines.forEachIndexed { index, machine ->
        MachineRow(
            machine = machine,
            readiness = state.readiness[machine.deviceId] ?: Readiness(),
            first = index == 0,
            onUnlock = { onUnlock(machine) },
            onCheckAgain = { onCheckAgain(machine) },
            onForget = { onForget(machine) },
        )
    }
    TextButton(
        onClick = onPairAnother,
        modifier = Modifier.shortcut(AppAction.PAIR_AGAIN, onInvoke = onPairAnother).focusRing(),
    ) {
        Text(stringResource(R.string.pair_again))
    }
}

@Composable
private fun MachineRow(
    machine: Machine,
    readiness: Readiness,
    first: Boolean,
    onUnlock: () -> Unit,
    onCheckAgain: () -> Unit,
    onForget: () -> Unit,
) {
    var confirmForget by remember { mutableStateOf(false) }
    if (confirmForget) {
        ForgetDialog(machine, onConfirm = { confirmForget = false; onForget() }, onDismiss = { confirmForget = false })
    }
    val unlockFor = stringResource(R.string.unlock_machine_named, machine.label())
    Column(
        verticalArrangement = Arrangement.spacedBy(Spacing.small),
        modifier = Modifier.testTag("machine_row_${machine.deviceId}"),
    ) {
        Text(
            machine.label(),
            style = MaterialTheme.typography.titleLarge,
            modifier = Modifier.semantics { heading() },
        )
        // Always shown: the name is the machine's own word and two machines can share one.
        Text(
            shortDeviceId(machine.deviceId),
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        ReadinessChip(readiness, onCheckNow = onCheckAgain)
        Row {
            val unlockModifier = if (first) {
                Modifier
                    .focusRequester(rememberAutoFocusRequester())
                    .shortcut(AppAction.UNLOCK, onInvoke = onUnlock)
            } else {
                Modifier.shortcut(AppAction.UNLOCK_THIS, onInvoke = onUnlock)
            }
            Button(
                onClick = onUnlock,
                modifier = unlockModifier.semantics { contentDescription = unlockFor }.focusRing(),
            ) {
                Icon(Icons.Filled.Lock, contentDescription = null)
                Spacer(Modifier.width(Spacing.small))
                Text(stringResource(R.string.unlock_action))
            }
            Spacer(Modifier.width(Spacing.small))
            TextButton(
                onClick = { confirmForget = true },
                modifier = Modifier.shortcut(AppAction.FORGET) { confirmForget = true }.focusRing(),
            ) {
                Text(stringResource(R.string.forget_action))
            }
        }
    }
}

/** Asks before a machine is taken off the phone, and says what that does and what it does not. */
@Composable
internal fun ForgetDialog(machine: Machine, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.forget_title, machine.label())) },
        text = { Text(stringResource(R.string.forget_message)) },
        confirmButton = {
            TextButton(onClick = onConfirm, modifier = Modifier.shortcut(AppAction.FORGET_CONFIRM, onInvoke = onConfirm).focusRing()) {
                Text(stringResource(R.string.forget_confirm))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, modifier = Modifier.shortcut(AppAction.FORGET_KEEP, onInvoke = onDismiss).focusRing()) {
                Text(stringResource(R.string.forget_cancel))
            }
        },
    )
}
