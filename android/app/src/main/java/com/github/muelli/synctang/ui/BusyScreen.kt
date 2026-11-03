// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.Column
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.Progress
import com.github.muelli.synctang.Stage
import com.github.muelli.synctang.stageDetail
import com.github.muelli.synctang.R
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester
import kotlinx.coroutines.delay

/**
 * A spinner, and a Cancel button when [onCancel] is given. A dial can
 * run for a minute and a half against a machine that is not waiting,
 * which is too long to offer nothing but the back gesture.
 *
 * With [progress] it also says what is going on: one line for how far it has
 * got, which is announced politely when it changes, and under it the time spent
 * and, after the first, the attempt number. That second line is deliberately not
 * a live region: a counter announced every second would drown out everything
 * else a screen reader has to say. Nothing here takes focus; Cancel is as it was.
 */
@Composable
internal fun Busy(
    message: String,
    machine: Machine? = null,
    progress: Progress? = null,
    clock: () -> Long = System::currentTimeMillis,
    // Last, so that a trailing lambda is still the Cancel action.
    onCancel: (() -> Unit)? = null,
) {
    machine?.let { MachineName(it) }
    Row(verticalAlignment = Alignment.CenterVertically) {
        CircularProgressIndicator(modifier = Modifier.size(32.dp))
        Spacer(Modifier.width(Spacing.between))
        Column {
            // Announced as it appears, since nothing else on this screen
            // takes focus for a screen reader to land on.
            Text(
                message,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
            )
            if (progress != null) BusyDetail(progress, clock)
        }
    }
    if (onCancel != null) {
        OutlinedButton(
            onClick = onCancel,
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(rememberAutoFocusRequester())
                .shortcut(AppAction.CANCEL, onInvoke = onCancel)
                .focusRing(),
        ) {
            Text(stringResource(R.string.busy_cancel))
        }
    }
}

/** The stage line, and the time and attempt under it. */
@Composable
private fun BusyDetail(progress: Progress, clock: () -> Long) {
    if (progress.stage != Stage.NONE) {
        Text(
            stringResource(stageDetail(progress)),
            style = MaterialTheme.typography.bodyMedium,
            // Announced when it changes, so a long wait is heard to move on.
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        )
    }
    var now by remember(progress.startedAtMillis) { mutableLongStateOf(clock()) }
    LaunchedEffect(progress.startedAtMillis) {
        while (true) {
            now = clock()
            delay(1_000)
        }
    }
    val seconds = ((now - progress.startedAtMillis) / 1_000).coerceAtLeast(0)
    val parts = listOfNotNull(
        stringResource(R.string.busy_elapsed, seconds.toInt()),
        if (progress.attempt > 1) stringResource(R.string.busy_attempt, progress.attempt) else null,
    )
    // Not a live region (see Busy), and the plain text is what is read if someone
    // navigates to it.
    Text(
        parts.joinToString(" \u00b7 "),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.testTag(BUSY_ELAPSED_TAG),
    )
}

/** Marks the elapsed-time line, so a test can find it without knowing its words. */
internal const val BUSY_ELAPSED_TAG = "busy-elapsed"
