// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.test.platform.app.InstrumentationRegistry
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The list of paired machines as it is kept on the phone, in the real encrypted file: what goes in comes
 * back from another instance (a fresh start of the app reads it that way), in the order of last use, with
 * the mark that says a machine needs pairing again.
 */
class PairingStoreTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val a = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "study", "https://d.example.net/v2/")
    private val b = Machine("BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB", "attic")
    private val before = PairingStore(context).machines()

    private fun clear() {
        val store = PairingStore(context)
        store.machines().forEach { store.forget(it.deviceId) }
    }

    @Before
    fun startEmpty() = clear()

    @After
    fun putBack() {
        clear()
        val store = PairingStore(context)
        before.reversed().forEach { store.saveMachine(it) }
    }

    @Test
    fun machinesComeBackFromAnotherInstanceMostRecentFirst() {
        PairingStore(context).saveMachine(a)
        PairingStore(context).saveMachine(b)
        assertEquals(listOf(b, a), PairingStore(context).machines())
    }

    @Test
    fun savingAMachineAgainUpdatesItAndMakesItTheFirst() {
        val store = PairingStore(context)
        store.saveMachine(a)
        store.saveMachine(b)
        store.saveMachine(a.copy(name = "renamed"))
        assertEquals(listOf("renamed", "attic"), PairingStore(context).machines().map { it.name })
    }

    @Test
    fun markingAMachineUsedMovesItToTheFront() {
        val store = PairingStore(context)
        store.saveMachine(a)
        store.saveMachine(b)
        store.markUsed(a.deviceId)
        assertEquals(listOf(a.deviceId, b.deviceId), PairingStore(context).machines().map { it.deviceId })
    }

    @Test
    fun forgettingRemovesOnlyThatMachine() {
        val store = PairingStore(context)
        store.saveMachine(a)
        store.saveMachine(b)
        store.forget(a.deviceId)
        assertEquals(listOf(b), PairingStore(context).machines())
        assertEquals(null, PairingStore(context).machine(a.deviceId))
    }

    @Test
    fun aNewIdentityKeepsTheMachinesAndMarksEachOfThem() {
        val store = PairingStore(context)
        store.saveMachine(a)
        store.saveMachine(b)
        val seed = store.identitySeed()
        store.startNewIdentity()

        val after = PairingStore(context)
        assertEquals(listOf(b.deviceId, a.deviceId), after.machines().map { it.deviceId })
        assertTrue(after.machines().all { it.needsPairing })
        assertTrue("the identity seed was kept", after.identitySeed() != seed)
        // Names and discovery servers survive the mark.
        assertEquals("study", after.machine(a.deviceId)?.name)
        assertEquals("https://d.example.net/v2/", after.machine(a.deviceId)?.discovery)
    }

    @Test
    fun pairingAMachineAgainClearsItsMark() {
        val store = PairingStore(context)
        store.saveMachine(a.copy(needsPairing = true))
        store.saveMachine(a)
        assertEquals(listOf(false), PairingStore(context).machines().map { it.needsPairing })
    }
}
