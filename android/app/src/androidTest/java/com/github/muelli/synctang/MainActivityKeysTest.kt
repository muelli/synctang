// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.SystemClock
import android.view.KeyEvent
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Keys pressed on the real activity, as a hardware keyboard sends them:
 * with their modifiers, through the window system, to the app as it is
 * installed. The screens' wiring is checked on its own in
 * [ScreenShortcutsTest]; this is the path from a key to the screen.
 *
 * Starts from whatever the app is showing, so it needs a screen that
 * offers About: the pairing screen of an app not paired yet. On a phone
 * whose key Android keeps in software the app stops at a refusal
 * screen first, and the test is skipped there.
 */
class MainActivityKeysTest {

    private val instrumentation = InstrumentationRegistry.getInstrumentation()

    private fun press(keyCode: Int, meta: Int = 0) {
        val now = SystemClock.uptimeMillis()
        for (action in listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP)) {
            instrumentation.sendKeySync(KeyEvent(now, now, action, keyCode, 0, meta))
        }
        instrumentation.waitForIdleSync()
    }

    private fun ActivityScenario<MainActivity>.title(): String {
        var title = ""
        onActivity { title = it.title.toString() }
        return title
    }

    private fun ActivityScenario<MainActivity>.waitForTitle(contains: String): String {
        val end = System.currentTimeMillis() + 5_000
        while (System.currentTimeMillis() < end && !title().contains(contains)) Thread.sleep(100)
        return title()
    }

    @Test
    fun controlIOpensAboutAndEscapeClosesIt() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val before = scenario.waitForTitle("Pair with a machine")
            assumeTrue("not on a screen that offers About: $before", before.contains("Pair with a machine"))

            press(KeyEvent.KEYCODE_I, KeyEvent.META_CTRL_ON)
            assertTrue(scenario.waitForTitle("About this phone").contains("About this phone"))

            press(KeyEvent.KEYCODE_ESCAPE)
            assertEquals(before, scenario.waitForTitle("Pair with a machine"))
        }
    }

    @Test
    fun theLetterAloneDoesNothing() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val before = scenario.waitForTitle("Pair with a machine")
            assumeTrue(before.contains("Pair with a machine"))

            press(KeyEvent.KEYCODE_I)
            assertEquals(before, scenario.title())
        }
    }

    @Test
    fun enrolByHandHasItsOwnKey() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val before = scenario.waitForTitle("Pair with a machine")
            assumeTrue(before.contains("Pair with a machine"))

            press(KeyEvent.KEYCODE_E, KeyEvent.META_CTRL_ON)
            assertTrue(scenario.waitForTitle("About this phone").contains("About this phone"))
        }
    }
}
