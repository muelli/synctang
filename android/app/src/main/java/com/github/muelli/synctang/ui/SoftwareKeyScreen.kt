// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.focusRing

/**
 * Shown when Android has made this phone's key in software. The action
 * lowers the protection, so it is not the focused one and Enter does not
 * reach it by reflex; back leaves the app without allowing anything.
 */
@Composable
internal fun SoftwareKeyScreen(onAllow: () -> Unit) {
    StatusHeader(
        Icons.Filled.Warning,
        MaterialTheme.colorScheme.error,
        stringResource(R.string.software_key_title),
        stringResource(R.string.software_key_body),
    )
    OutlinedButton(
        onClick = onAllow,
        modifier = Modifier
            .fillMaxWidth()
            .focusRing(),
    ) {
        Text(stringResource(R.string.software_key_allow))
    }
    Text(stringResource(R.string.software_key_leave_hint), style = MaterialTheme.typography.bodySmall)
}
