// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Readiness
import com.github.muelli.synctang.readinessAnnouncement
import com.github.muelli.synctang.readinessDotColour
import com.github.muelli.synctang.readinessLabel

/**
 * Whether the paired machine looks like it is waiting, from what it announced to the discovery
 * server: a coloured dot AND words, so that colour is never the only signal. It says what
 * discovery says, not that an unlock will work, and it never disables or hides Unlock.
 *
 * Announced politely when the state changes and not otherwise: the words shown may carry an age
 * that moves on every poll, so what a screen reader is given is [readinessAnnouncement], which
 * has no age and so does not change with it. The dot is decoration. Nothing here takes focus.
 */
@Composable
internal fun ReadinessChip(readiness: Readiness) {
    val label = readinessLabel(readiness)
    val shown = stringResource(label.res, *label.args.toTypedArray())
    val announced = stringResource(readinessAnnouncement(readiness.state))
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.testTag(READINESS_CHIP_TAG),
    ) {
        Box(
            Modifier
                .size(12.dp)
                .clip(CircleShape)
                .background(Color(readinessDotColour(readiness.state)))
                .clearAndSetSemantics { },
        )
        Spacer12()
        Text(
            shown,
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.semantics {
                contentDescription = announced
                liveRegion = LiveRegionMode.Polite
            },
        )
    }
}

@Composable
private fun Spacer12() = Box(Modifier.size(12.dp))

/** Marks the chip, so a test can find it without knowing its words. */
internal const val READINESS_CHIP_TAG = "readiness-chip"
