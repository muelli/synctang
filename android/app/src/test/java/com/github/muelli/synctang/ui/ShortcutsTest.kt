// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The shortcut table is the one place a key is bound to an action, so
 * what a keyboard user can rely on is pinned here: one chord per
 * meaning, the same chord for the same kind of action on every screen,
 * and no action quietly left without a key.
 */
class ShortcutsTest {

    @Test
    fun theMainActionOfEveryScreenSharesOneChord() {
        val primaries = AppAction.entries.filter { it.role == Role.PRIMARY }
        assertTrue(primaries.size > 1)
        assertEquals(setOf(Chord.CTRL_ENTER), primaries.map { it.chord }.toSet())
    }

    @Test
    fun backingOutSharesEscape() {
        val backs = AppAction.entries.filter { it.role == Role.BACK }
        assertTrue(backs.size > 1)
        assertEquals(setOf(Chord.ESCAPE), backs.map { it.chord }.toSet())
    }

    @Test
    fun everyOtherActionHasItsOwnChord() {
        val others = AppAction.entries.filter { it.role == Role.OTHER && it.chord != null }
        val chords = others.map { it.chord }
        assertEquals("duplicate chord among $others", chords.size, chords.toSet().size)
        assertFalse(Chord.CTRL_ENTER in chords)
        assertFalse(Chord.ESCAPE in chords)
    }

    /**
     * Allowing a key that Android would hold in software weakens a
     * protection, and making a new key throws the old one away. Each has to be
     * chosen on purpose, by reaching the button, never by a chord remembered
     * from another screen.
     */
    @Test
    fun onlyTheDestructiveOrWeakeningChoicesHaveNoShortcut() {
        assertEquals(
            listOf(AppAction.ALLOW_SOFTWARE_KEY, AppAction.REPLACE_KEY),
            AppAction.entries.filter { it.chord == null },
        )
    }

    @Test
    fun everyActionNamesItself() {
        assertTrue(AppAction.entries.all { it.label != 0 })
    }

    @Test
    fun chordsUseControlExceptEscape() {
        assertTrue(AppAction.entries.mapNotNull { it.chord }.filter { it != Chord.ESCAPE }.all { it.ctrl })
    }

    @Test
    fun aChordMatchesExactlyItsModifiers() {
        val copy = AppAction.COPY_COMMAND.chord!!
        assertTrue(copy.matches(KeyEvent.KEYCODE_C, ctrl = true, shift = true, alt = false, meta = false))
        assertFalse(copy.matches(KeyEvent.KEYCODE_C, ctrl = true, shift = false, alt = false, meta = false))
        assertFalse(copy.matches(KeyEvent.KEYCODE_C, ctrl = false, shift = true, alt = false, meta = false))
        assertFalse(copy.matches(KeyEvent.KEYCODE_C, ctrl = true, shift = true, alt = true, meta = false))
        assertFalse(copy.matches(KeyEvent.KEYCODE_V, ctrl = true, shift = true, alt = false, meta = false))
    }

    /** Meta-slash is Android's own shortcut list, and Alt-Tab its own switcher. */
    @Test
    fun metaAndAltAreLeftToTheSystem() {
        val p = AppAction.PAIR_AGAIN.chord!!
        assertTrue(p.matches(KeyEvent.KEYCODE_P, ctrl = true, shift = false, alt = false, meta = false))
        assertFalse(p.matches(KeyEvent.KEYCODE_P, ctrl = true, shift = false, alt = false, meta = true))
        assertFalse(p.matches(KeyEvent.KEYCODE_P, ctrl = true, shift = false, alt = true, meta = false))
    }

    @Test
    fun theEnterKeyOnTheNumberPadCountsAsEnter() {
        assertTrue(
            Chord.CTRL_ENTER.matches(KeyEvent.KEYCODE_NUMPAD_ENTER, ctrl = true, shift = false, alt = false, meta = false),
        )
    }

    @Test
    fun escapeNeedsNoModifier() {
        assertTrue(Chord.ESCAPE.matches(KeyEvent.KEYCODE_ESCAPE, ctrl = false, shift = false, alt = false, meta = false))
        assertFalse(Chord.ESCAPE.matches(KeyEvent.KEYCODE_ESCAPE, ctrl = true, shift = false, alt = false, meta = false))
    }

    @Test
    fun keyLabelsReadAsPeopleWriteThem() {
        assertEquals("Ctrl+Enter", Chord.CTRL_ENTER.describe())
        assertEquals("Esc", Chord.ESCAPE.describe())
        assertEquals("Ctrl+Shift+C", AppAction.COPY_COMMAND.chord!!.describe())
        assertEquals("Ctrl+P", AppAction.PAIR_AGAIN.chord!!.describe())
        assertEquals("Ctrl+1", AppAction.TOGGLE_DEVICE_ID.chord!!.describe())
    }
}

class ShortcutRegistryTest {

    private fun ShortcutRegistry.press(keyCode: Int, ctrl: Boolean = false, shift: Boolean = false) =
        handle(keyCode, ctrl, shift, alt = false, meta = false)

    @Test
    fun aRegisteredActionRunsOnItsChord() {
        val registry = ShortcutRegistry()
        var ran = 0
        registry.register(AppAction.UNLOCK) { ran++ }
        assertTrue(registry.press(KeyEvent.KEYCODE_ENTER, ctrl = true))
        assertEquals(1, ran)
    }

    @Test
    fun aKeyNobodyRegisteredIsLeftAlone() {
        val registry = ShortcutRegistry()
        registry.register(AppAction.UNLOCK) {}
        assertFalse(registry.press(KeyEvent.KEYCODE_ENTER))
        assertFalse(registry.press(KeyEvent.KEYCODE_P, ctrl = true))
    }

    @Test
    fun unregisteringStopsIt() {
        val registry = ShortcutRegistry()
        var ran = 0
        val off = registry.register(AppAction.UNLOCK) { ran++ }
        off()
        assertFalse(registry.press(KeyEvent.KEYCODE_ENTER, ctrl = true))
        assertEquals(0, ran)
        assertTrue(registry.available.isEmpty())
    }

    /**
     * A screen that replaces another registers its own before the old
     * one has been disposed; the old one's late unregistration must not
     * take the new one away.
     */
    @Test
    fun aLateUnregistrationDoesNotRemoveItsReplacement() {
        val registry = ShortcutRegistry()
        var second = 0
        val offFirst = registry.register(AppAction.UNLOCK) {}
        registry.register(AppAction.UNLOCK) { second++ }
        offFirst()
        assertTrue(registry.press(KeyEvent.KEYCODE_ENTER, ctrl = true))
        assertEquals(1, second)
    }

    /** Where two registered actions share a chord, the one declared first answers. */
    @Test
    fun whenTwoShareEscapeTheEarlierDeclaredActionAnswers() {
        val registry = ShortcutRegistry()
        var closed = 0
        var dismissed = 0
        registry.register(AppAction.DISMISS) { dismissed++ }
        registry.register(AppAction.CLOSE_ABOUT) { closed++ }
        assertTrue(registry.press(KeyEvent.KEYCODE_ESCAPE))
        assertEquals(1, closed)
        assertEquals(0, dismissed)
    }

    @Test
    fun availableListsOnlyWhatIsRegistered() {
        val registry = ShortcutRegistry()
        registry.register(AppAction.UNLOCK) {}
        registry.register(AppAction.OPEN_ABOUT) {}
        assertEquals(listOf(AppAction.UNLOCK, AppAction.OPEN_ABOUT), registry.available)
    }

    @Test
    fun anActionWithNoChordIsNeverReachedByKey() {
        val registry = ShortcutRegistry()
        var ran = 0
        registry.register(AppAction.ALLOW_SOFTWARE_KEY) { ran++ }
        assertFalse(registry.press(KeyEvent.KEYCODE_ENTER, ctrl = true))
        assertFalse(registry.press(KeyEvent.KEYCODE_ESCAPE))
        assertEquals(0, ran)
        assertNull(AppAction.ALLOW_SOFTWARE_KEY.chord)
    }
}
