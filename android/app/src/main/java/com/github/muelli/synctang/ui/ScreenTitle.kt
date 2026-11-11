// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.annotation.StringRes
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState

/**
 * What the screen is called, which is what a screen reader announces
 * when it appears and what the task switcher shows. It is the
 * screen's own heading wherever it has one, so that what is heard and
 * what is read match.
 */
@StringRes
internal fun screenTitle(state: UnlockUiState, aboutOpen: Boolean): Int = when {
    aboutOpen -> R.string.about_title
    else -> when (state) {
        is UnlockUiState.NeedsPairing -> R.string.pair_title
        is UnlockUiState.Ready -> R.string.ready_title
        is UnlockUiState.Machines -> R.string.machines_title
        is UnlockUiState.Connecting -> R.string.unlock_connecting
        is UnlockUiState.Pairing -> R.string.pair_working
        is UnlockUiState.Approve -> R.string.unlock_request_title
        is UnlockUiState.Working -> R.string.unlock_working
        is UnlockUiState.Done -> R.string.done_title
        is UnlockUiState.Failed -> R.string.error_title
        is UnlockUiState.SoftwareKeyRefused -> R.string.software_key_title
        is UnlockUiState.KeyNeedsReplacing -> R.string.key_attention_title
    }
}
