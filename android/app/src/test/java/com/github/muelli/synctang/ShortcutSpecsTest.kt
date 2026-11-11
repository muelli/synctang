// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the launcher is told about the machines: one shortcut each, as many as it shows, named for the machine.
 * The name is the machine's own word and nothing proves it, so it is made safe and short before it goes
 * anywhere a person reads it without the app's own screens around it.
 */
class ShortcutSpecsTest {
    private val a = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "study")
    private val b = Machine("BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB", "attic")
    private val c = Machine("CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC-CCCCCCC", "cellar")

    @Test
    fun noMachinesNoShortcuts() {
        assertEquals(emptyList<ShortcutSpec>(), shortcutSpecs(emptyList(), 4))
    }

    @Test
    fun oneShortcutForEachMachineInTheOrderGiven() {
        val specs = shortcutSpecs(listOf(b, a), 4)
        assertEquals(listOf("attic", "study"), specs.map { it.label })
        assertEquals(listOf(b.deviceId, a.deviceId), specs.map { it.deviceId })
    }

    @Test
    fun theIdOfAShortcutIsForTheMachineAndNothingElse() {
        val ids = shortcutSpecs(listOf(a, b), 4).map { it.id }
        assertEquals(2, ids.toSet().size)
        assertTrue(ids.all { it.startsWith("unlock-") })
    }

    @Test
    fun noMoreThanTheLauncherShows() {
        assertEquals(listOf("study", "attic"), shortcutSpecs(listOf(a, b, c), 2).map { it.label })
        assertEquals(emptyList<ShortcutSpec>(), shortcutSpecs(listOf(a), 0))
    }

    @Test
    fun aMachineListedTwiceGetsOneShortcut() {
        assertEquals(1, shortcutSpecs(listOf(a, a.copy(name = "again")), 4).size)
    }

    @Test
    fun controlCharactersAndDirectionOverridesAreTakenOutOfTheName() {
        val hostile = a.copy(name = "st\u001b[2Judy\n‮evil\u0007")
        assertEquals("st[2Judyevil", shortcutSpecs(listOf(hostile), 4).single().label)
    }

    @Test
    fun aLongNameIsCutAndSaysSo() {
        val label = shortcutSpecs(listOf(a.copy(name = "x".repeat(100))), 4).single().label
        assertEquals("x".repeat(23) + "…", label)
    }

    @Test
    fun runsOfWhiteSpaceAreOneSpace() {
        assertEquals("my server", shortcutSpecs(listOf(a.copy(name = "  my \t  server  ")), 4).single().label)
    }

    @Test
    fun aMachineWithNoUsableNameIsNamedByTheStartOfItsDeviceId() {
        assertEquals("AAAAAAA", shortcutSpecs(listOf(a.copy(name = "")), 4).single().label)
        assertEquals("AAAAAAA", shortcutSpecs(listOf(a.copy(name = "\u001b\u0007")), 4).single().label)
    }
}
