// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.KeyProblem
import com.github.muelli.synctang.R
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

/**
 * The key is not whole, and throwing it away is not the app's call. Trying
 * again is what focus starts on, since it can only help; making a new key is
 * the second button, has no key of its own, and says what it costs.
 */
@Composable
internal fun KeyNeedsReplacingScreen(problem: KeyProblem, onTryAgain: () -> Unit, onReplace: () -> Unit) {
    StatusHeader(
        Icons.Filled.Warning,
        MaterialTheme.colorScheme.error,
        stringResource(R.string.key_attention_title),
        stringResource(
            when (problem) {
                KeyProblem.HARDWARE_HALF_MISSING -> R.string.key_attention_hardware
                KeyProblem.FILES_DAMAGED -> R.string.key_attention_files
                KeyProblem.FILES_DO_NOT_MATCH -> R.string.key_attention_mismatch
            },
        ),
    )
    Button(
        onClick = onTryAgain,
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .shortcut(AppAction.RETRY, onInvoke = onTryAgain)
            .focusRing(),
    ) {
        Text(stringResource(R.string.error_retry))
    }
    Text(stringResource(R.string.key_attention_consequence), style = MaterialTheme.typography.bodyMedium)
    OutlinedButton(
        onClick = onReplace,
        modifier = Modifier
            .fillMaxWidth()
            .shortcut(AppAction.REPLACE_KEY, onInvoke = onReplace)
            .focusRing(),
    ) {
        Text(stringResource(R.string.key_attention_replace))
    }
}
