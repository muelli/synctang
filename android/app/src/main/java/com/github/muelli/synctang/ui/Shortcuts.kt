// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import android.view.KeyEvent
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.semantics
import com.github.muelli.synctang.R

/**
 * A key combination. Control plus a key, so that nothing here is typed
 * into a text field by accident, except Escape, which stands alone.
 * Alt and Meta are never part of a chord: Android keeps Meta-slash for
 * its own list of shortcuts, and Alt-Tab for switching apps.
 */
internal data class Chord(val keyCode: Int, val ctrl: Boolean = true, val shift: Boolean = false) {

    fun matches(keyCode: Int, ctrl: Boolean, shift: Boolean, alt: Boolean, meta: Boolean): Boolean {
        if (alt || meta || ctrl != this.ctrl || shift != this.shift) return false
        return keyCode == this.keyCode || (this.keyCode == KeyEvent.KEYCODE_ENTER && keyCode == KeyEvent.KEYCODE_NUMPAD_ENTER)
    }

    /** For a person to read, as on a keyboard shortcut list: "Ctrl+Shift+C". */
    fun describe(): String = buildList {
        if (ctrl) add("Ctrl")
        if (shift) add("Shift")
        add(keyName())
    }.joinToString("+")

    private fun keyName(): String = when (keyCode) {
        KeyEvent.KEYCODE_ENTER -> "Enter"
        KeyEvent.KEYCODE_ESCAPE -> "Esc"
        in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> ('A' + (keyCode - KeyEvent.KEYCODE_A)).toString()
        in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> ('0' + (keyCode - KeyEvent.KEYCODE_0)).toString()
        else -> keyCode.toString()
    }

    companion object {
        val CTRL_ENTER = Chord(KeyEvent.KEYCODE_ENTER)
        val ESCAPE = Chord(KeyEvent.KEYCODE_ESCAPE, ctrl = false)
        fun ctrl(keyCode: Int) = Chord(keyCode)
        fun ctrlShift(keyCode: Int) = Chord(keyCode, shift = true)
    }
}

/**
 * What an action means on whatever screen it appears, which is what
 * decides its key: the main action of a screen is always Ctrl+Enter and
 * backing out of one is always Escape, so neither has to be learnt per
 * screen.
 */
internal enum class Role { PRIMARY, BACK, OTHER }

/**
 * Every control that can be pressed, and the key that presses it. This
 * is the one table: the screens attach an action to the control, the
 * activity lists the table's entries on screen in Android's shortcut
 * list, and the tests walk it.
 *
 * Where two actions on screen share a chord, the one declared first
 * answers, which is why dismissing the undo bar is declared last: while
 * it is up, Escape leaves the screen underneath rather than the bar.
 */
internal enum class AppAction(val role: Role, val chord: Chord?, @StringRes val label: Int) {
    UNLOCK(Role.PRIMARY, Chord.CTRL_ENTER, R.string.unlock_action),
    APPROVE(Role.PRIMARY, Chord.CTRL_ENTER, R.string.unlock_approve),
    UNLOCK_AGAIN(Role.PRIMARY, Chord.CTRL_ENTER, R.string.unlock_again),
    RETRY(Role.PRIMARY, Chord.CTRL_ENTER, R.string.error_retry),
    SCAN_AGAIN(Role.PRIMARY, Chord.CTRL_ENTER, R.string.error_scan_again),
    PAIR(Role.PRIMARY, Chord.CTRL_ENTER, R.string.pair_manual_action),

    CANCEL(Role.BACK, Chord.ESCAPE, R.string.busy_cancel),
    DECLINE(Role.BACK, Chord.ESCAPE, R.string.unlock_decline),
    KEEP_CURRENT(Role.BACK, Chord.ESCAPE, R.string.shortcut_keep_current),

    /** Escape closes the question whether to forget a machine, without forgetting it. */
    FORGET_KEEP(Role.BACK, Chord.ESCAPE, R.string.forget_cancel),
    CLOSE_ABOUT(Role.BACK, Chord.ESCAPE, R.string.about_close),

    PAIR_AGAIN(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_P), R.string.pair_again),

    /** A machine's own Unlock on the list: the first row's is [UNLOCK]; no key for the others. */
    UNLOCK_THIS(Role.OTHER, null, R.string.unlock_action),

    /** No key, on purpose: it takes a machine off the list. */
    FORGET(Role.OTHER, null, R.string.forget_action),

    /** No key either: the answer that forgets is reached by pressing it. */
    FORGET_CONFIRM(Role.OTHER, null, R.string.forget_confirm),
    CHECK_AGAIN(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_R), R.string.readiness_check_again),
    OPEN_ABOUT(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_I), R.string.about_title),
    ENROL_BY_HAND(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_E), R.string.pair_by_hand),
    COPY_COMMAND(Role.OTHER, Chord.ctrlShift(KeyEvent.KEYCODE_C), R.string.pair_copy),
    TOGGLE_DETAILS(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_T), R.string.error_details_show),
    TOGGLE_DEVICE_ID(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_1), R.string.shortcut_toggle_device_id),
    TOGGLE_KEY_ID(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_2), R.string.shortcut_toggle_key_id),
    GRANT_CAMERA(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_G), R.string.pair_grant_camera),
    OPEN_SETTINGS(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_O), R.string.pair_open_settings),
    UNDO(Role.OTHER, Chord.ctrl(KeyEvent.KEYCODE_Z), R.string.pair_undo),

    /**
     * No key, on purpose: accepting a key Android keeps in software
     * weakens a protection, so it is chosen by reaching the button and
     * pressing it, not by a chord learnt on another screen.
     */
    ALLOW_SOFTWARE_KEY(Role.OTHER, null, R.string.software_key_allow),

    /** No key, for the same reason: it throws a key away. */
    REPLACE_KEY(Role.OTHER, null, R.string.key_attention_replace),

    DISMISS(Role.BACK, Chord.ESCAPE, R.string.pair_undo_dismiss),
}

/**
 * The actions currently on screen and what each does. Not state in the
 * Compose sense: key events are looked up here when they arrive, from
 * the activity, so they work whether or not focus is anywhere in the
 * window.
 */
internal class ShortcutRegistry {
    private val handlers = LinkedHashMap<AppAction, () -> Unit>()

    /** What is on screen now, in the table's order. */
    val available: List<AppAction> get() = AppAction.entries.filter { it in handlers }

    /** Returns the call that takes it away again. */
    fun register(action: AppAction, handler: () -> Unit): () -> Unit {
        handlers[action] = handler
        // Identity, so that a screen being replaced cannot take away
        // the registration of the one replacing it.
        return { if (handlers[action] === handler) handlers.remove(action) }
    }

    /** Whether the key was one of ours, and so is consumed. */
    fun handle(keyCode: Int, ctrl: Boolean, shift: Boolean, alt: Boolean, meta: Boolean): Boolean {
        val action = AppAction.entries.firstOrNull {
            it in handlers && it.chord?.matches(keyCode, ctrl, shift, alt, meta) == true
        } ?: return false
        handlers.getValue(action).invoke()
        return true
    }
}

internal val LocalShortcuts = compositionLocalOf { ShortcutRegistry() }

/** Which action a node carries, for tests to find every control that has none. */
internal val ShortcutAction = SemanticsPropertyKey<AppAction>("ShortcutAction")
private var SemanticsPropertyReceiver.shortcutAction by ShortcutAction

/**
 * Binds [action] to this control for as long as it is on screen and
 * [enabled]; the key then does what pressing the control does.
 */
@Composable
internal fun Modifier.shortcut(action: AppAction, enabled: Boolean = true, onInvoke: () -> Unit): Modifier {
    val registry = LocalShortcuts.current
    val current by rememberUpdatedState(onInvoke)
    DisposableEffect(registry, action, enabled) {
        if (!enabled) return@DisposableEffect onDispose {}
        val off = registry.register(action) { current() }
        onDispose(off)
    }
    return semantics { shortcutAction = action }
}
