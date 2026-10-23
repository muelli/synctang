// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import com.github.muelli.synctang.R
import com.github.muelli.synctang.Retry
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun FailedScreen(state: UnlockUiState.Failed, model: UnlockViewModel) {
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
