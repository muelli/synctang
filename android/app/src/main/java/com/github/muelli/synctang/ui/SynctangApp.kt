// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.github.muelli.synctang.BackAction
import com.github.muelli.synctang.BiometricGate
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.backAction
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape

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
internal fun UndoReplace(ready: UnlockUiState.Ready?, snackbar: SnackbarHostState, model: UnlockViewModel) {
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
internal fun UndoSnackbarHost(state: SnackbarHostState) {
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
