// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import android.app.Activity
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.paneTitle
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.github.muelli.synctang.BackAction
import com.github.muelli.synctang.BiometricGate
import com.github.muelli.synctang.R
import com.github.muelli.synctang.UnlockUiState
import com.github.muelli.synctang.UnlockViewModel
import com.github.muelli.synctang.backAction
import com.github.muelli.synctang.focusRing
import com.github.muelli.synctang.rememberAutoFocusRequester

@Composable
internal fun SynctangApp(gate: BiometricGate, shortcuts: ShortcutRegistry, model: UnlockViewModel = viewModel()) {
    CompositionLocalProvider(LocalShortcuts provides shortcuts) {
        SynctangScreens(gate, model)
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun SynctangScreens(gate: BiometricGate, model: UnlockViewModel) {
    val state by model.state.collectAsState()

    // About this phone is a flag rather than a route: there is one
    // such screen, and it is only offered where nothing is under way.
    var aboutOpen by rememberSaveable { mutableStateOf(false) }
    val aboutFor = aboutIdentity(state)
    LaunchedEffect(aboutFor == null) {
        if (aboutFor == null) aboutOpen = false
    }
    val about = aboutFor?.takeIf { aboutOpen }
    BackHandler(enabled = about != null) { aboutOpen = false }

    // Only intercepted where something is waiting on this phone; on
    // every other screen back leaves the app as usual.
    val back = backAction(state)
    BackHandler(enabled = about == null && back != BackAction.EXIT) {
        when (back) {
            BackAction.CANCEL -> model.cancel()
            BackAction.KEEP_CURRENT_MACHINE -> model.keepCurrentMachine()
            BackAction.IGNORE, BackAction.EXIT -> Unit
        }
    }

    // The window is named for the screen it shows: the app, then the
    // screen. That is what the task switcher lists and what a screen
    // reader announces when it changes.
    val screen = stringResource(screenTitle(state, aboutOpen = about != null))
    val windowTitle = stringResource(R.string.window_title, stringResource(R.string.app_name), screen)
    val activity = LocalContext.current as? Activity
    SideEffect { activity?.title = windowTitle }

    val snackbar = remember { SnackbarHostState() }
    UndoForget(forgottenIn(state), snackbar, model)

    Scaffold(
        // Test tags become resource IDs, so that adb and UI Automator
        // can find an element by name rather than by where it happens
        // to be on the screen.
        modifier = Modifier.semantics { testTagsAsResourceId = true },
        topBar = {
            AppBar(
                aboutOpen = about != null,
                onOpenAbout = if (aboutFor != null) ({ aboutOpen = true }) else null,
                onCloseAbout = { aboutOpen = false },
            )
        },
        snackbarHost = { UndoSnackbarHost(snackbar) },
        // The Surface around this already paints the background.
        containerColor = Color.Transparent,
    ) { padding ->
        ScreenColumn(Modifier.padding(padding).semantics { paneTitle = screen }) {
            if (about != null) {
                AboutScreen(about, appVersion())
                return@ScreenColumn
            }
            when (val current = state) {
                is UnlockUiState.NeedsPairing ->
                    PairingScreen(current, model::pair, model::keepCurrentMachine, onEnrolByHand = { aboutOpen = true })
                is UnlockUiState.Ready -> ReadyScreen(
                    current,
                    { model.connect(gate) },
                    model::startPairing,
                    { model.checkAgain() },
                    { model.forget(current.machine.deviceId) },
                )
                is UnlockUiState.Machines -> MachinesScreen(
                    current,
                    { machine -> model.connect(machine, gate) },
                    { machine -> model.checkAgain(machine.deviceId) },
                    { machine -> model.forget(machine.deviceId) },
                    model::startPairing,
                )
                is UnlockUiState.Connecting ->
                    Busy(stringResource(R.string.unlock_connecting), current.machine, current.progress, onCancel = model::cancel)
                is UnlockUiState.Pairing ->
                    Busy(stringResource(R.string.pair_working), current.machine, current.progress, onCancel = model::cancel)
                is UnlockUiState.Working ->
                    Busy(stringResource(R.string.unlock_working), current.machine, current.progress)
                is UnlockUiState.Approve -> ApproveScreen(current, { model.approve(gate) }, model::cancel)
                is UnlockUiState.Done -> DoneScreen(current, model::refresh)
                is UnlockUiState.Failed -> FailedScreen(current, model::refresh, model::startPairing)
                is UnlockUiState.SoftwareKeyRefused -> SoftwareKeyScreen(model::allowSoftwareKey)
                is UnlockUiState.KeyNeedsReplacing ->
                    KeyNeedsReplacingScreen(current.problem, model::refresh, model::replaceKey)
            }
        }
    }
}

/**
 * The app's name, or About this phone with a way back. [onOpenAbout]
 * is null where About is not offered.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun AppBar(
    aboutOpen: Boolean,
    onOpenAbout: (() -> Unit)?,
    onCloseAbout: () -> Unit,
    // A parameter so that the listing screenshots, which are made from the debug build, can
    // say what the release says.
    appName: String = stringResource(R.string.app_name),
) {
    TopAppBar(
        title = {
            Text(if (aboutOpen) stringResource(R.string.about_title) else appName)
        },
        navigationIcon = {
            if (aboutOpen) {
                IconButton(
                    onClick = onCloseAbout,
                    modifier = Modifier
                        .focusRequester(rememberAutoFocusRequester())
                        .shortcut(AppAction.CLOSE_ABOUT, onInvoke = onCloseAbout)
                        .focusRing(),
                ) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.about_close))
                }
            }
        },
        actions = {
            if (!aboutOpen && onOpenAbout != null) {
                IconButton(
                    onClick = onOpenAbout,
                    modifier = Modifier.shortcut(AppAction.OPEN_ABOUT, onInvoke = onOpenAbout).focusRing(),
                ) {
                    Icon(Icons.Outlined.Info, contentDescription = stringResource(R.string.about_title))
                }
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(containerColor = Color.Transparent),
    )
}

@Composable
private fun appVersion(): String {
    val context = LocalContext.current
    return remember(context) {
        runCatching { context.packageManager.getPackageInfo(context.packageName, 0).versionName }
            .getOrNull()
            .orEmpty()
    }
}

/** The machine that has just been forgotten, which the home screens offer to take back. */
internal fun forgottenIn(state: UnlockUiState): com.github.muelli.synctang.Machine? = when (state) {
    is UnlockUiState.Ready -> state.forgotten
    is UnlockUiState.Machines -> state.forgotten
    is UnlockUiState.NeedsPairing -> state.forgotten
    else -> null
}

/**
 * A machine that has just been forgotten is offered back, the way Android apps offer to undo anything done
 * in one tap: a snackbar with Undo. Forgetting only takes it off this phone, so taking it back is all there
 * is to undo.
 */
@Composable
internal fun UndoForget(forgotten: com.github.muelli.synctang.Machine?, snackbar: SnackbarHostState, model: UnlockViewModel) {
    val message = forgotten?.let { stringResource(R.string.forgot_machine, it.label()) }
    val undo = stringResource(R.string.pair_undo)
    LaunchedEffect(forgotten) {
        if (message == null) return@LaunchedEffect
        val result = snackbar.showSnackbar(
            message,
            actionLabel = undo,
            withDismissAction = true,
            duration = SnackbarDuration.Long,
        )
        when (result) {
            SnackbarResult.ActionPerformed -> model.undoForget()
            SnackbarResult.Dismissed -> model.dismissUndo()
        }
    }
}

@Composable
internal fun UndoSnackbarHost(state: SnackbarHostState) {
    SnackbarHost(state) { data ->
        Snackbar(
            modifier = Modifier.padding(12.dp),
            action = {
                TextButton(
                    onClick = { data.performAction() },
                    modifier = Modifier.shortcut(AppAction.UNDO) { data.performAction() }.focusRing(),
                ) {
                    Text(data.visuals.actionLabel.orEmpty())
                }
            },
            dismissAction = {
                TextButton(
                    onClick = { data.dismiss() },
                    modifier = Modifier.shortcut(AppAction.DISMISS) { data.dismiss() }.focusRing(),
                ) {
                    Text(stringResource(R.string.pair_undo_dismiss))
                }
            },
        ) {
            Text(data.visuals.message)
        }
    }
}
