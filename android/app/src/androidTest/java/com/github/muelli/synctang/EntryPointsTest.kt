// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.ComponentName
import android.content.Intent
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test

/**
 * The ways into the app other than its icon: the shortcuts on a long press, and what the widget will send.
 * Run on the app as installed, with a machine saved in its own store (nothing dials: the machine is not
 * there, and the screens that matter are the ones before that is found out).
 */
class EntryPointsTest {

    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val machine = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "attic")
    private val before = PairingStore(context).machine()

    @Before
    fun pairWithAMachine() {
        PairingStore(context).saveMachine(machine)
    }

    @After
    fun putBackWhatWasThere() {
        // The store has no way to forget one, and the other tests start from an app that is not paired (they skip
        // otherwise), so a phone that had none is put back to that by the call that drops the machine with the
        // identity; one that had a machine gets it back.
        if (before == null) PairingStore(context).startNewIdentity() else PairingStore(context).saveMachine(before)
    }

    private fun title(scenario: ActivityScenario<MainActivity>): String {
        var title = ""
        scenario.onActivity { title = it.title.toString() }
        return title
    }

    private fun waitForTitle(scenario: ActivityScenario<MainActivity>, text: String): String {
        val end = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < end && !title(scenario).contains(text)) Thread.sleep(100)
        return title(scenario)
    }

    // These two do not need the app to get as far as the Unlock screen, which a phone or an emulator whose key
    // is in software does not (the app stops at a refusal): they read what the flow was told, from the app's log.

    @Test
    fun whatTheEntryActivityIsGivenReachesTheFlowOfTheApp() {
        AppLogReader.clear()
        context.startActivity(UnlockEntryActivity.intentFor(context, machine.deviceId).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        assertTrue("the flow was never asked to unlock ${machine.deviceId}; log:\n${AppLogReader.read()}", AppLogReader.waitFor("action unlock requested from outside id=${machine.deviceId}"))
    }

    @Test
    fun theExportedActivityIsNotAWayInEvenWithTheSameAction() {
        AppLogReader.clear()
        val intent = Intent(context, MainActivity::class.java)
            .setAction(UnlockEntryActivity.ACTION_UNLOCK)
            .putExtra(UnlockEntryActivity.EXTRA_MACHINE, machine.deviceId)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        ActivityScenario.launch<MainActivity>(intent).use {
            // The app has started (its flow logs that it was asked to refresh), and was not asked to unlock.
            assertTrue("the app never started; log:\n${AppLogReader.read()}", AppLogReader.waitFor("action refresh"))
            Thread.sleep(1_500)
            assertFalse("an intent from outside asked the flow to unlock:\n${AppLogReader.read()}", AppLogReader.read().contains("unlock requested from outside"))
        }
    }

    @Test
    fun theEntryActivityIsNotExportedAndTheMainOneIs() {
        val pm = context.packageManager
        assertFalse(pm.getActivityInfo(ComponentName(context, UnlockEntryActivity::class.java), 0).exported)
        assertTrue(pm.getActivityInfo(ComponentName(context, MainActivity::class.java), 0).exported)
    }

    @Test
    fun theLauncherIsOfferedAShortcutForThePairedMachine() {
        LauncherShortcuts.publish(context, listOf(machine))
        val ids = ShortcutManagerCompat.getDynamicShortcuts(context).map { it.id }
        assertEquals(listOf("unlock-${machine.deviceId}"), ids)
        val label = ShortcutManagerCompat.getDynamicShortcuts(context).single().shortLabel.toString()
        assertTrue(label, label.contains("attic"))
    }

    @Test
    fun theShortcutsFollowTheMachinesAndTheirNames() {
        LauncherShortcuts.publish(context, listOf(machine))
        LauncherShortcuts.publish(context, emptyList())
        assertTrue(ShortcutManagerCompat.getDynamicShortcuts(context).isEmpty())
    }

    @Test
    fun theShortcutsIntentOpensTheAppAndStartsTheUnlockOfThatMachine() {
        val connecting = context.getString(R.string.unlock_connecting)
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val first = waitForTitle(scenario, context.getString(R.string.ready_title))
            assumeTrue("not on the Unlock screen: $first (a key problem stops the app before it)", first.contains(context.getString(R.string.ready_title)))

            // What the launcher does with the shortcut: starts the entry activity, from outside the app's task.
            context.startActivity(UnlockEntryActivity.intentFor(context, machine.deviceId).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            assertTrue(title(scenario), waitForTitle(scenario, connecting).contains(connecting))
        }
    }

    @Test
    fun anIntentToTheMainActivityAloneCannotStartAnUnlock() {
        val ready = context.getString(R.string.ready_title)
        val intent = Intent(context, MainActivity::class.java)
            .setAction(UnlockEntryActivity.ACTION_UNLOCK)
            .putExtra(UnlockEntryActivity.EXTRA_MACHINE, machine.deviceId)
        ActivityScenario.launch<MainActivity>(intent).use { scenario ->
            val first = waitForTitle(scenario, ready)
            assumeTrue("not on the Unlock screen: $first", first.contains(ready))
            Thread.sleep(1_500)
            assertTrue("an intent from outside started an unlock: ${title(scenario)}", title(scenario).contains(ready))
        }
    }
}
