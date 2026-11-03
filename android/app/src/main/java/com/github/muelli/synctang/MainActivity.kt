// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.Bundle
import android.view.KeyEvent
import android.view.KeyboardShortcutGroup
import android.view.KeyboardShortcutInfo
import android.view.Menu
import androidx.activity.compose.setContent
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.ViewModelProvider
import com.github.muelli.synctang.ui.ShortcutRegistry
import com.github.muelli.synctang.ui.SynctangApp
import com.github.muelli.synctang.ui.SynctangTheme

/**
 * The whole app: one screen that is either "pair with a machine" or
 * "unlock the machine you paired with".
 *
 * Deliberately not ported from the reference app this project borrowed
 * its transport from: the quick settings tile, the home screen widget,
 * and the build-a-second-APK-per-machine trick. The plan for this app
 * asks for one Unlock action, and every one of those features is
 * another entry point that has to be got right in front of a key that
 * unlocks a disk.
 */
class MainActivity : FragmentActivity() {

    /** What the screens on show can be told to do by key; see [ShortcutRegistry]. */
    private val shortcuts = ShortcutRegistry()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The gate is an interface, so test mode swaps it rather than
        // threading a flag through the unlock flow: there is then no
        // branch inside the flow that could behave differently in the
        // build people actually use.
        val gate: BiometricGate =
            if (TestMode.noBiometric) OpenGate else BiometricGateImpl(this)
        setContent {
            SynctangTheme {
                SynctangApp(gate, shortcuts)
            }
        }
    }

    /**
     * Keys are taken here, before whatever has focus sees them, so they
     * work on a screen where nothing is focused (a spinner, a keyboard
     * just attached) and cannot be swallowed by a text field. A held
     * key does not repeat an action: Approve must take one press.
     */
    /**
     * Back from Settings, say, where a finger may have been enrolled and the key
     * retired: the key is checked again, in the background.
     */
    override fun onStop() {
        // Not asking about the machine while nobody is looking.
        ViewModelProvider(this)[UnlockViewModel::class.java].movedToBackground()
        super.onStop()
    }

    override fun onStart() {
        super.onStart()
        ViewModelProvider(this)[UnlockViewModel::class.java].returnedToForeground()
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN &&
            event.repeatCount == 0 &&
            shortcuts.handle(event.keyCode, event.isCtrlPressed, event.isShiftPressed, event.isAltPressed, event.isMetaPressed)
        ) {
            return true
        }
        return super.dispatchKeyEvent(event)
    }

    /** What Android shows for Meta+/: the keys of the screen as it is now. */
    override fun onProvideKeyboardShortcuts(data: MutableList<KeyboardShortcutGroup>, menu: Menu?, deviceId: Int) {
        super.onProvideKeyboardShortcuts(data, menu, deviceId)
        val infos = shortcuts.available.mapNotNull { action ->
            val chord = action.chord ?: return@mapNotNull null
            val modifiers = (if (chord.ctrl) KeyEvent.META_CTRL_ON else 0) or
                (if (chord.shift) KeyEvent.META_SHIFT_ON else 0)
            KeyboardShortcutInfo(getString(action.label), chord.keyCode, modifiers)
        }
        if (infos.isNotEmpty()) {
            data.add(0, KeyboardShortcutGroup(getString(R.string.shortcut_group_title), infos))
        }
    }
}
