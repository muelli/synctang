// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun DoneScreen(state: UnlockUiState.Done, onDone: () -> Unit) {
    StatusHeader(
        Icons.Filled.CheckCircle,
        MaterialTheme.colorScheme.primary,
        stringResource(R.string.done_title),
        stringResource(R.string.unlock_done, state.machine.label()),
    )
    Button(
        onClick = onDone,
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_again))
    }
}
