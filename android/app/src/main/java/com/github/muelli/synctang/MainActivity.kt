// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Intent
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
 * its transport from: the quick settings tile and the build-a-second-APK-
 * per-machine trick. Every entry point is another way to reach a key that
 * unlocks a disk, so the ones there are (a shortcut on the launcher and a
 * widget) share one rule: this activity is exported, so it reads nothing
 * from the intent that starts it. A request to unlock comes through
 * [UnlockEntryActivity], which only this app's own shortcuts and pending
 * intents can start, and through [PendingUnlock]; it is the Unlock button's
 * one tap for a machine the phone is paired with, and the fingerprint is
 * still what lets the answer go.
 */
class MainActivity : FragmentActivity() {

    /** What the screens on show can be told to do by key; see [ShortcutRegistry]. */
    private val shortcuts = ShortcutRegistry()

    private lateinit var gate: BiometricGate

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // The gate is an interface, so test mode swaps it rather than
        // threading a flag through the unlock flow: there is then no
        // branch inside the flow that could behave differently in the
        // build people actually use.
        gate = if (TestMode.noBiometric) OpenGate else BiometricGateImpl(this)
        setContent {
            SynctangTheme {
                SynctangApp(gate, shortcuts)
            }
        }
        askedToUnlock()
    }

    /** The app was already open and a shortcut or the widget brought it to the front. */
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        askedToUnlock()
    }

    /** Takes what the entry activity left, if anything: never anything from this activity's own intent. */
    private fun askedToUnlock() {
        val deviceId = PendingUnlock.app.take() ?: return
        ViewModelProvider(this)[UnlockViewModel::class.java].unlockRequested(deviceId, gate)
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
