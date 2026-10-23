// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.selection.SelectionContainer
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
import androidx.compose.ui.text.font.FontFamily
import com.github.muelli.synctang.R
import com.github.muelli.synctang.Retry
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun FailedScreen(state: UnlockUiState.Failed, onRetry: () -> Unit, onScanAgain: () -> Unit) {
    state.machine?.let { MachineHeading(it) }
    Text(stringResource(R.string.error_title), style = MaterialTheme.typography.headlineSmall)
    SelectionContainer {
        Text(
            state.message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
        )
    }
    // The machine's own reason, where it gave one.
    state.detail?.let { detail ->
        SelectionContainer {
            Text(detail, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
        }
    }
    // Of no use to most people, but the one thing worth quoting in a
    // bug report, so it is selectable.
    state.technical?.let { technical ->
        var showDetails by rememberSaveable { mutableStateOf(false) }
        TextButton(onClick = { showDetails = !showDetails }, modifier = Modifier.focusRing()) {
            Text(stringResource(if (showDetails) R.string.error_details_hide else R.string.error_details_show))
        }
        if (showDetails) {
            SelectionContainer {
                Text(technical, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
            }
        }
    }
    // A pairing code is single-use, so retrying a failed pairing means
    // a fresh scan, not a dial against a machine that never enrolled
    // this phone.
    val scanAgain = state.retry == Retry.SCAN_AGAIN
    Button(
        onClick = if (scanAgain) onScanAgain else onRetry,
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Text(stringResource(if (scanAgain) R.string.error_scan_again else R.string.error_retry))
    }
}
