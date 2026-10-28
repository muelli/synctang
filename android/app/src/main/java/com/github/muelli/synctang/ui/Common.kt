// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.KeyHolderKey
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.R
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.onEscape

/** Spacing shared by every screen, so that they line up with each other. */
internal object Spacing {
    val screen = 24.dp
    val between = 16.dp
    val small = 8.dp
    val heroHeight = 64.dp
    val statusIcon = 48.dp
}

internal fun Machine.label(): String = name.ifBlank { deviceId }

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
            .padding(Spacing.screen),
        verticalArrangement = Arrangement.spacedBy(Spacing.between),
        content = content,
    )
}

/** The top of every screen: what it is, and in a sentence what to do. */
@Composable
internal fun ScreenHeader(title: String, explanation: String? = null) {
    Column(verticalArrangement = Arrangement.spacedBy(Spacing.small)) {
        Text(title, style = MaterialTheme.typography.headlineSmall)
        explanation?.let {
            Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** The top of a screen that reports an outcome: a glyph, a headline, then the detail. */
@Composable
internal fun StatusHeader(icon: ImageVector, tint: Color, headline: String, detail: String?) {
    Column(verticalArrangement = Arrangement.spacedBy(Spacing.small)) {
        Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(Spacing.statusIcon))
        Text(headline, style = MaterialTheme.typography.headlineSmall)
        detail?.let {
            SelectionContainer {
                Text(it, style = MaterialTheme.typography.bodyLarge)
            }
        }
    }
}

/** The machine a screen is about, by name: the one it gave, which proves nothing. */
@Composable
internal fun MachineName(machine: Machine) {
    if (machine.label().isBlank()) return
    Column {
        Text(stringResource(R.string.unlock_machine), style = MaterialTheme.typography.labelMedium)
        Text(machine.label(), style = MaterialTheme.typography.titleLarge)
    }
}

/**
 * Who is asking, in two parts: whether the identity the machine proved
 * during the handshake is the one this phone was paired with, and that
 * identity itself. The name alone proves nothing; the machine chose it.
 */
@Composable
internal fun MachineIdentity(machine: Machine, peerId: String, onEscape: () -> Unit) {
    MachineName(machine)
    val matches = sameDeviceId(peerId, machine.deviceId)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(
            if (matches) Icons.Filled.CheckCircle else Icons.Filled.Warning,
            contentDescription = null,
            tint = if (matches) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
            modifier = Modifier.size(20.dp),
        )
        Spacer(Modifier.width(Spacing.small))
        Text(
            stringResource(if (matches) R.string.identity_matches else R.string.identity_mismatch),
            style = MaterialTheme.typography.bodyMedium,
        )
    }
    Identifier(stringResource(R.string.unlock_request_identity), peerId, shortDeviceId(peerId), onEscape)
}

/**
 * A long identifier in its short form, with the full value one tap
 * away and selectable, for comparing against what a machine prints.
 */
@Composable
internal fun Identifier(label: String, full: String, short: String, onEscape: () -> Unit = {}) {
    var expanded by rememberSaveable(full) { mutableStateOf(false) }
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        SelectionContainer {
            Text(
                if (expanded) full else short,
                style = MaterialTheme.typography.bodyMedium,
                fontFamily = FontFamily.Monospace,
            )
        }
        if (short != full) {
            TextButton(
                onClick = { expanded = !expanded },
                modifier = Modifier.onEscape(onEscape).focusRing(),
            ) {
                Text(stringResource(if (expanded) R.string.identifier_show_less else R.string.identifier_show_all))
            }
        }
    }
}

/** A value in full, for the About screen. */
@Composable
internal fun Labelled(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        SelectionContainer {
            Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
        }
    }
}

/** The one thing a screen exists for, taller than the rest so it reads as such. */
internal fun Modifier.heroAction(): Modifier = fillMaxWidth().heightIn(min = Spacing.heroHeight)

@Composable
internal fun Identity.backingDescription(): String = when (backing) {
    KeyHolderKey.Backing.STRONGBOX -> stringResource(R.string.identity_backing_strongbox)
    KeyHolderKey.Backing.TEE -> stringResource(R.string.identity_backing_tee)
    KeyHolderKey.Backing.SOFTWARE -> stringResource(R.string.identity_backing_software)
}

internal fun Context.copyToClipboard(text: String) {
    val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    clipboard.setPrimaryClip(ClipData.newPlainText("synctang", text))
}
