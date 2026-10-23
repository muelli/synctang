// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.KeyHolderKey
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.R

internal fun Machine.label(): String = name.ifBlank { deviceId }

@Composable
internal fun MachineHeading(machine: Machine) {
    Labelled(
        stringResource(R.string.unlock_machine),
        machine.name.ifBlank { machine.deviceId },
    )
}

@Composable
internal fun Labelled(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        SelectionContainer {
            Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
        }
    }
}

@Composable
internal fun Identity.backingDescription(): String = when (backing) {
    KeyHolderKey.Backing.STRONGBOX -> stringResource(R.string.identity_backing_strongbox)
    KeyHolderKey.Backing.TEE_OR_SOFTWARE -> stringResource(R.string.identity_backing_tee)
}

internal fun Context.copyToClipboard(text: String) {
    val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    clipboard.setPrimaryClip(ClipData.newPlainText("synctang", text))
}

@Composable
internal fun SynctangTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = darkColorScheme()) {
        Surface(modifier = Modifier.fillMaxSize(), content = content)
    }
}

/** The scrolling column every screen is laid out in. */
@Composable
internal fun ScreenColumn(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
        content = content,
    )
}
