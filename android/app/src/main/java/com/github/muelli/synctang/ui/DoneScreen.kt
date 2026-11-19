// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.purposeLabel
import com.github.muelli.synctang.rememberBuzzer
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun DoneScreen(state: UnlockUiState.Done, onDone: () -> Unit) {
    // One buzz when the unlock is done, and not another when the screen is made again after a turn of the phone.
    val buzzer = rememberBuzzer()
    var buzzed by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        if (!buzzed) buzzer.unlocked()
        buzzed = true
    }
    StatusHeader(
        Icons.Filled.CheckCircle,
        MaterialTheme.colorScheme.primary,
        stringResource(R.string.done_title),
        if (state.purpose.isEmpty()) {
            stringResource(R.string.unlock_done, state.machine.label())
        } else {
            // Not "can now unlock its disk": the machine said it would do something else with the answer.
            val words = purposeLabel(state.purpose).let { stringResource(it.res, *it.args.toTypedArray()) }
            stringResource(R.string.done_other, state.machine.label(), words)
        },
    )
    Button(
        onClick = onDone,
        modifier = Modifier
            .fillMaxWidth()
            .focusRequester(rememberAutoFocusRequester())
            .shortcut(AppAction.UNLOCK_AGAIN, onInvoke = onDone)
            .focusRing(),
    ) {
        Text(stringResource(R.string.unlock_again))
    }
}
