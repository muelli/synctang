// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape
import com.github.muelli.synctang.rememberAutoFocusRequester

/**
 * A spinner, and a Cancel button when [onCancel] is given. A dial can
 * run for a minute and a half against a machine that is not waiting,
 * which is too long to offer nothing but the back gesture.
 */
@Composable
internal fun Busy(message: String, onCancel: (() -> Unit)? = null) {
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
