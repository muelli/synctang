// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * The unlock flow's state machine, driven through fakes of the Go
 * bridge, the store and the biometric prompt.
 *
 * Three defects found by reading the code are pinned here. A pairing
 * saved the machine before dialling, so a failed one left a machine
 * that had never enrolled this phone on the Unlock screen, with no way
 * back short of clearing the app's data, and overwrote any machine
 * that had been working. Back on a screen where the machine was
 * waiting finished the activity without telling it. And a dial or a
 * pairing could not be cancelled at all.
 */
class UnlockFlowTest {

    private val mainExecutor = Executors.newSingleThreadExecutor()
    private val main = mainExecutor.asCoroutineDispatcher()
    private val scope = CoroutineScope(SupervisorJob() + main)

    private val store = FakeStore()
    private val sessions = Collections.synchronizedList(mutableListOf<FakeSession>())
    private val pairings = Collections.synchronizedList(mutableListOf<FakePairing>())
    private var nextSession: () -> FakeSession = { FakeSession() }
    private var nextPairing: () -> FakePairing = { FakePairing() }

    private val identity = Identity("PHONE-ID", "04ab", KeyHolderKey.Backing.TEE_OR_SOFTWARE)

    private val flow = UnlockFlow(
        scope = scope,
        io = Dispatchers.IO,
        store = store,
        identity = { identity },
        newSession = { nextSession().also { sessions += it } },
        newPairing = { nextPairing().also { pairings += it } },
        agree = { _, _ -> ByteArray(32) },
        strings = object : Strings {
            override fun get(id: Int, vararg args: Any) = "res:$id"
        },
    )

    @After
    fun tearDown() {
        scope.cancel()
        mainExecutor.shutdownNow()
    }

    private fun onMain(block: () -> Unit) = runBlocking(main) { block() }

    private inline fun <reified T : UnlockUiState> await(): T {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            val now = flow.state.value
            if (now is T) return now
            Thread.sleep(5)
        }
        fail("expected ${T::class.simpleName}, still ${flow.state.value}")
        throw AssertionError()
    }

    private val machineA = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "a")
    private val machineB = Machine("BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB", "b")
    private fun payload(machine: Machine, code: String? = "CODE") =
        "synctang://pair?machine=${machine.deviceId}&name=${machine.name}" + (code?.let { "&psk=$it" } ?: "")

    // Pairing (defect 1)

    @Test
    fun aPairingWithACodeIsNotSavedUntilTheMachineHasEnrolledThePhone() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        var accepted = false
        onMain { accepted = flow.pair(payload(machineB)) }
        assertTrue(accepted)
        await<UnlockUiState.Pairing>()
        pairings.awaitOne().started.await(5, TimeUnit.SECONDS)
        assertNull("saved before the dial succeeded", store.saved)

        pairings.single().finish()
        assertEquals(machineB.deviceId, await<UnlockUiState.Ready>().machine.deviceId)
        assertEquals(machineB.deviceId, store.saved?.deviceId)
    }

    @Test
    fun aFailedPairingOffersToScanAgainAndSavesNothing() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        nextPairing = { FakePairing(failWith = Exception("mobile: gave up pairing")) }

        onMain { flow.pair(payload(machineB)) }
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertNull(store.saved)

        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()
    }

    @Test
    fun aFailedPairingWithAnotherMachineKeepsTheOneThatWorks() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextPairing = { FakePairing(failWith = Exception("mobile: the machine refused the pairing")) }

        onMain { flow.startPairing() }
        assertEquals(machineA, await<UnlockUiState.NeedsPairing>().previous)
        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.Failed>()
        assertEquals(machineA, store.saved)

        onMain { flow.startPairing() }
        assertEquals(machineA, await<UnlockUiState.NeedsPairing>().previous)
        onMain { flow.keepCurrentMachine() }
        assertEquals(machineA, await<UnlockUiState.Ready>().machine)
    }

    @Test
    fun readyCanGoToThePairingScreenAndBackWithoutLosingAnything() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()

        onMain { flow.startPairing() }
        val pairing = await<UnlockUiState.NeedsPairing>()
        assertEquals(machineA, pairing.previous)
        assertEquals(BackAction.KEEP_CURRENT_MACHINE, backAction(pairing))

        onMain { flow.keepCurrentMachine() }
        assertEquals(machineA, await<UnlockUiState.Ready>().machine)
        assertEquals(machineA, store.saved)
    }

    @Test
    fun aPayloadWithoutACodeIsSavedAtOnce() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB, code = null)) }
        assertEquals(machineB.deviceId, await<UnlockUiState.Ready>().machine.deviceId)
        assertEquals(machineB.deviceId, store.saved?.deviceId)
        assertTrue(pairings.isEmpty())
    }

    @Test
    fun somethingThatIsNotAPayloadIsRejected() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        var accepted = true
        onMain { accepted = flow.pair("hello") }
        assertFalse(accepted)
        assertTrue(flow.state.value is UnlockUiState.NeedsPairing)
        assertNull(store.saved)
    }

    // Undoing a replacement

    @Test
    fun aPayloadWithoutACodeThatReplacesAMachineCanBeUndone() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB, code = null)) }
        val replaced = await<UnlockUiState.Ready>()
        assertEquals(machineB.deviceId, replaced.machine.deviceId)
        assertEquals(machineA, replaced.replaced)

        onMain { flow.undoReplace() }
        awaitReady { it.machine == machineA && it.replaced == null }
        assertEquals(machineA, store.saved)
    }

    @Test
    fun dismissingTheUndoKeepsTheNewMachine() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()

        onMain { flow.pair(payload(machineB, code = null)) }
        awaitReady { it.replaced == machineA }
        onMain { flow.dismissUndo() }
        awaitReady { it.replaced == null }
        assertEquals(machineB.deviceId, store.saved?.deviceId)
    }

    @Test
    fun aSuccessfulPairingThatReplacesAMachineCanBeUndoneToo() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB)) }
        pairings.awaitOne().finish()
        awaitReady { it.replaced == machineA }
        onMain { flow.undoReplace() }
        awaitReady { it.machine == machineA && it.replaced == null }
        assertEquals(machineA, store.saved)
    }

    @Test
    fun pairingTheSameMachineAgainHasNothingToUndo() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()

        onMain { flow.pair(payload(machineA, code = null)) }
        Thread.sleep(100)
        assertNull(await<UnlockUiState.Ready>().replaced)
    }

    @Test
    fun aFirstPairingHasNothingToUndo() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB, code = null)) }
        assertNull(await<UnlockUiState.Ready>().replaced)
    }

    private fun awaitReady(condition: (UnlockUiState.Ready) -> Boolean): UnlockUiState.Ready {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            val now = flow.state.value
            if (now is UnlockUiState.Ready && condition(now)) return now
            Thread.sleep(5)
        }
        fail("Ready never matched, still ${flow.state.value}")
        throw AssertionError()
    }

    // Cancel (defect 4)

    @Test
    fun cancellingAPairingStopsItAndReturnsToTheScanner() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB)) }
        assertEquals(BackAction.CANCEL, backAction(await<UnlockUiState.Pairing>()))
        pairings.awaitOne().started.await(5, TimeUnit.SECONDS)

        onMain { flow.cancel() }
        assertEquals(machineA, await<UnlockUiState.NeedsPairing>().previous)
        assertTrue("the Go pairing was not cancelled", pairings.single().cancelled)
        assertEquals(machineA, store.saved)
    }

    @Test
    fun aCancelThatArrivesBeforeThePairingStartsStillStopsIt() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        onMain {
            flow.pair(payload(machineB))
            flow.cancel()
        }
        await<UnlockUiState.NeedsPairing>()
        assertTrue(pairings.all { it.cancelled })
        assertNull(store.saved)
    }

    @Test
    fun cancellingAConnectStopsTheDialAndReturnsToReady() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()

        onMain { flow.connect() }
        assertEquals(BackAction.CANCEL, backAction(await<UnlockUiState.Connecting>()))
        val session = sessions.awaitOne()
        session.started.await(5, TimeUnit.SECONDS)

        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
        assertTrue("the Go dial was not cancelled", session.cancelled)
        assertTrue(session.closed)
    }

    @Test
    fun aCancelThatLosesTheRaceWithTheDialStillTellsTheMachine() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        // The dial completes even though it was cancelled, as a real one
        // does when the cancel lands after the request has arrived.
        nextSession = { FakeSession(cancelEndsConnectWithSuccess = true) }

        onMain { flow.connect() }
        val session = sessions.awaitOne()
        session.started.await(5, TimeUnit.SECONDS)
        onMain { flow.cancel() }

        await<UnlockUiState.Ready>()
        assertEquals(1, session.declines.size)
        assertTrue(session.closed)
    }

    @Test
    fun aDialThatFailsOffersToTryAgain() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(failWith = Exception("mobile: gave up dialling")) }

        onMain { flow.connect() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.REFRESH, failed.retry)
        assertEquals("mobile: gave up dialling", failed.message)
    }

    @Test
    fun closingTheFlowCancelsADialStillRunning() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()

        onMain { flow.connect() }
        val session = sessions.awaitOne()
        session.started.await(5, TimeUnit.SECONDS)
        onMain { flow.close() }
        assertTrue(session.cancelled)
    }

    // Refuse and back on a waiting machine (defect 3)

    private fun connectToARequest(): FakeSession {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true) }
        onMain { flow.connect() }
        await<UnlockUiState.Approve>()
        return sessions.single()
    }

    @Test
    fun refusingARequestTellsTheMachine() {
        val session = connectToARequest()
        assertEquals(BackAction.CANCEL, backAction(flow.state.value))

        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
        assertEquals(listOf(DECLINED_BY_KEY_HOLDER), session.declines.toList())
        assertTrue(session.closed)
    }

    @Test
    fun aLockedOutBiometricDeclinesAndSaysSo() {
        val session = connectToARequest()

        onMain { flow.approve { _, _ -> AuthOutcome.LOCKED_OUT } }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_biometric_lockout}", failed.message)
        assertEquals(1, session.declines.size)
        assertTrue(session.closed)
    }

    @Test
    fun anApprovedRequestIsAnswered() {
        val session = connectToARequest()

        onMain { flow.approve { _, _ -> AuthOutcome.SUCCEEDED } }
        await<UnlockUiState.Done>()
        assertTrue(session.answered)
        assertTrue(session.declines.isEmpty())
    }

    @Test
    fun backIsOnlyInterceptedWhereSomethingIsWaitingOnThePhone() {
        val m = machineA
        assertEquals(BackAction.EXIT, backAction(UnlockUiState.NeedsPairing(identity)))
        assertEquals(BackAction.KEEP_CURRENT_MACHINE, backAction(UnlockUiState.NeedsPairing(identity, m)))
        assertEquals(BackAction.EXIT, backAction(UnlockUiState.Ready(m, identity)))
        assertEquals(BackAction.EXIT, backAction(UnlockUiState.Done(m)))
        assertEquals(BackAction.EXIT, backAction(UnlockUiState.Failed(m, "x")))
        assertEquals(BackAction.EXIT, backAction(UnlockUiState.Failed(null, "x", Retry.SCAN_AGAIN)))
        assertEquals(BackAction.CANCEL, backAction(UnlockUiState.Connecting(m)))
        assertEquals(BackAction.CANCEL, backAction(UnlockUiState.Pairing(m)))
        assertEquals(BackAction.CANCEL, backAction(UnlockUiState.ConfirmCode(m, "peer", "123")))
        assertEquals(BackAction.CANCEL, backAction(UnlockUiState.Approve(m, "peer", "kid")))
        assertEquals(BackAction.IGNORE, backAction(UnlockUiState.Working(m)))
    }

    private fun <T> MutableList<T>.awaitOne(): T {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            synchronized(this) { if (isNotEmpty()) return first() }
            Thread.sleep(5)
        }
        fail("nothing was created")
        throw AssertionError()
    }
}

private class FakeStore(@Volatile var saved: Machine? = null) : MachineStore {
    override fun machine() = saved
    override fun saveMachine(machine: Machine) {
        saved = machine
    }
}

/**
 * A dial that blocks until it is cancelled, the way a dial to a machine
 * that is not answering does, unless told to return at once.
 */
private class FakeSession(
    private val connectReturnsAtOnce: Boolean = false,
    private val cancelEndsConnectWithSuccess: Boolean = false,
    private val failWith: Exception? = null,
) : MachineSession {
    val started = CountDownLatch(1)
    private val release = CountDownLatch(1)
    @Volatile var cancelled = false
    @Volatile var closed = false
    @Volatile var answered = false
    val declines: MutableList<String> = Collections.synchronizedList(mutableListOf())

    override fun connect() {
        started.countDown()
        failWith?.let { throw it }
        if (connectReturnsAtOnce) return
        if (!release.await(10, TimeUnit.SECONDS)) throw Exception("fake dial timed out")
        if (!cancelEndsConnectWithSuccess) throw Exception("mobile: cancelled")
    }

    override fun cancel() {
        cancelled = true
        release.countDown()
    }

    override fun close() {
        closed = true
        cancel()
    }

    override fun decline(reason: String) {
        declines += reason
    }

    override fun machineName() = "machine"
    override fun peerId() = "PEER"
    override fun confirmCodeChallenge() = ""
    override fun hasRequest() = true
    override fun requestKidHex() = "ab"
    override fun submitConfirmCode(code: String) = Unit
    override fun answer(agree: (ByteArray, ByteArray) -> ByteArray) {
        agree(ByteArray(32), ByteArray(32))
        answered = true
    }
}

private class FakePairing(private val failWith: Exception? = null) : PairingAttempt {
    val started = CountDownLatch(1)
    private val release = CountDownLatch(1)
    @Volatile var cancelled = false
    @Volatile private var succeed = false

    fun finish() {
        succeed = true
        release.countDown()
    }

    override fun pair(machine: Machine, code: String, identity: Identity) {
        started.countDown()
        failWith?.let { throw it }
        if (!release.await(10, TimeUnit.SECONDS)) throw Exception("fake pairing timed out")
        if (!succeed) throw Exception("mobile: cancelled")
    }

    override fun cancel() {
        cancelled = true
        release.countDown()
    }
}
