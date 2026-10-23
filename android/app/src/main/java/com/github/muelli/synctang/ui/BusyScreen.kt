// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Machine
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
internal fun Busy(message: String, machine: Machine? = null, onCancel: (() -> Unit)? = null) {
    machine?.let { MachineName(it) }
    Row(verticalAlignment = Alignment.CenterVertically) {
        CircularProgressIndicator(modifier = Modifier.size(32.dp))
        Spacer(Modifier.width(Spacing.between))
        Text(message, style = MaterialTheme.typography.titleMedium)
    }
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
