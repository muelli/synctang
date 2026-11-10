// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.Cipher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
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

    private val identity = Identity("PHONE-ID", "04" + "deadbeefcafe".repeat(10), KeyHolderKey.Backing.TEE)
    @Volatile private var prepareFails: Exception? = null
    @Volatile private var prepareBlocksOn: CountDownLatch? = null
    @Volatile private var keyCheckFails: Exception? = null
    @Volatile private var keyCheckWaitsFor: CountDownLatch? = null
    private val keyChecks = AtomicInteger()
    @Volatile private var unwrapFails: Exception? = null
    @Volatile private var unwrapTakesMillis = 0L
    private val preparedCiphers = Collections.synchronizedList(mutableListOf<Cipher>())
    private val unwrappedWith = Collections.synchronizedList(mutableListOf<Cipher>())
    @Volatile private var identityFails: Exception? = null
    @Volatile private var softwareKeyAllowances = 0
    @Volatile private var identityResets = 0
    private val logged = Collections.synchronizedList(mutableListOf<Throwable>())

    /** What the status check answers, and how often it was asked. */
    @Volatile private var statusAnswer = Readiness()
    private val statusCalls = AtomicInteger()

    /** When above zero the check answers "rate limited, wait this long" instead of [statusAnswer]. */
    @Volatile private var statusRateLimitedMillis = 0L

    /** While set, the check does not return until it is released: a lookup that is in flight. */
    @Volatile private var statusWaitsFor: CountDownLatch? = null

    /** Every line the flow writes through the facade, at the most verbose level. */
    private val logLines = Collections.synchronizedList(mutableListOf<String>())
    private val appLog = AppLog({ _, _, line, _ -> logLines += line }) { AppLog.Level.DEBUG }

    /** Added to the flow's clock, to let time pass without waiting for it. */
    private val clockOffsetMillis = java.util.concurrent.atomic.AtomicLong()

    private var flow = newFlow()

    /** A flow whose readiness poll runs every [pollMillis] and whose checks are [minGapMillis] apart at least. */
    private fun newFlow(pollMillis: Long = 20, minGapMillis: Long = 0) = UnlockFlow(
        clock = { System.currentTimeMillis() + clockOffsetMillis.get() },
        scope = scope,
        io = Dispatchers.IO,
        store = store,
        identity = { identityFails?.let { throw it } ?: identity },
        allowSoftwareKey = {
            softwareKeyAllowances++
            identityFails = null
        },
        newSession = { nextSession().also { sessions += it } },
        newPairing = { nextPairing().also { pairings += it } },
        prepareKey = {
            prepareFails?.let { throw it }
            prepareBlocksOn?.await()
            Cipher.getInstance("AES").also { preparedCiphers += it }
        },
        unwrapKey = { cipher ->
            unwrapFails?.let { throw it }
            if (unwrapTakesMillis > 0) Thread.sleep(unwrapTakesMillis)
            unwrappedWith += cipher
            ByteArray(32)
        },
        resetIdentity = {
            identityResets++
            identityFails = null
            store.saved = null
            "OLD-PHONE-ID"
        },
        approveWindowMillis = 1_500,
        pollIntervalMillis = 20,
        readinessPollMillis = pollMillis,
        readinessMinGapMillis = minGapMillis,
        readinessJitter = { 0.5 },
        checkStatus = { _ ->
            statusCalls.incrementAndGet()
            statusWaitsFor?.await()
            if (statusRateLimitedMillis > 0) StatusAnswer.RateLimited(statusRateLimitedMillis) else StatusAnswer.Known(statusAnswer)
        },
        keyUseTimeoutSeconds = 1,
        checkKey = {
            keyChecks.incrementAndGet()
            keyCheckWaitsFor?.await()
            keyCheckFails?.let { throw it }
        },
        strings = object : Strings {
            override fun get(id: Int, vararg args: Any) =
                "res:$id" + if (args.isEmpty()) "" else args.joinToString(",", "(", ")")
        },
        log = { _, e -> logged += e },
        appLog = appLog,
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

    // The flow publishes an outcome and then closes the session in a finally, so a
    // test that has seen the outcome has not necessarily seen the close.
    private fun awaitClosed(session: FakeSession): Boolean {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (!session.closed && System.nanoTime() < deadline) Thread.sleep(5)
        return session.closed
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

    // The operator at the machine did not answer in time. The machine was reached, so the
    // phone must not say it could not be, and it says to pair again: if they confirmed
    // just after, the machine says the phone is already enrolled.
    @Test
    fun aPairingTheOperatorDidNotConfirmInTimeSaysSoAndNotUnreachable() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        nextPairing = { FakePairing(failWith = MachineFailure(FailureKind.PAIR_NOT_CONFIRMED, "", "mobile: the machine did not confirm", null)) }

        onMain { flow.pair(payload(machineB)) }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_pair_not_confirmed}", failed.message)
        assertNotEquals("res:${R.string.failure_pair_unreachable}", failed.message)
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertNull(store.saved)
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

    // A link with a setting the app does not understand is refused out loud, and nothing is
    // saved or dialled: ignoring it would pair against servers the owner chose not to use.
    @Test
    fun aLinkWithAnUnknownParameterIsRefusedAndSaysWhich() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()

        var accepted = false
        onMain { accepted = flow.pair(payload(machineB) + "&tor=1") }
        assertTrue("the refusal is shown as a failure, not as a mistyped link", accepted)
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals("res:${R.string.pair_unknown_parameters}(tor)", failed.message)
        assertEquals(machineA, store.saved)
        assertTrue(pairings.isEmpty())
    }

    // A setting given twice could mean one thing to one reader of the link and another to the next
    // (the command line tool refuses it too), so it is refused, and nothing is saved or dialled.
    @Test
    fun aLinkWithASettingGivenTwiceIsRefusedAndSaysWhich() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB) + "&name=other") }
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals("res:${R.string.pair_repeated_parameters}(name)", failed.message)
        assertNull(store.saved)
        assertTrue(pairings.isEmpty())
    }

    @Test
    fun aLinkWithARelaySettingIsRefusedAndSaysWhich() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB) + "&relay=https%3A%2F%2Fp.example.net%2Fendpoint") }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.pair_unknown_parameters}(relay)", failed.message)
        assertNull(store.saved)
        assertTrue(pairings.isEmpty())
    }

    @Test
    fun aLinkWithAnHttpDiscoveryServerWithoutInsecureIsRefusedAndSaysSo() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB) + "&discovery=http%3A%2F%2F10.0.0.6%3A8443%2Fv2%2F") }
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals("res:${R.string.pair_plain_http_discovery}", failed.message)
        assertNull(store.saved)
        assertTrue(pairings.isEmpty())
    }

    @Test
    fun aLinkWithABadSettingIsRefusedAndSaysWhich() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB) + "&discovery=ftp%3A%2F%2Fx.example.net%2F") }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.pair_bad_setting}(discovery)", failed.message)
        assertNull(store.saved)
        assertTrue(pairings.isEmpty())
    }

    // The machine's servers are stored with it and handed to the pairing and to every
    // later unlock, which is the point of carrying them in the link.
    @Test
    fun theMachinesServersReachThePairingAndAreSavedWithIt() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()

        onMain { flow.pair(payload(machineB) + "&discovery=https%3A%2F%2Fd.example.net%2Fv2%2F") }
        await<UnlockUiState.Pairing>()
        pairings.awaitOne().started.await(5, TimeUnit.SECONDS)
        assertEquals("https://d.example.net/v2/", pairings.single().machine?.discovery)

        pairings.single().finish()
        await<UnlockUiState.Ready>()
        assertEquals("https://d.example.net/v2/", store.saved?.discovery)
    }

    // The readiness chip: while the Ready screen is showing, and the app is in the foreground,
    // the machine's announcement is looked up every few seconds and put into the state.

    private fun awaitReadiness(condition: (Readiness) -> Boolean): UnlockUiState.Ready {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            val now = flow.state.value
            if (now is UnlockUiState.Ready && condition(now.readiness)) return now
            Thread.sleep(5)
        }
        fail("readiness never matched, still ${flow.state.value}")
        throw AssertionError()
    }

    @Test
    fun theReadyScreenFollowsTheMachinesStatusAndStartsAsChecking() {
        store.saved = machineA
        statusAnswer = Readiness(ReadinessState.WAITING, 12)
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        assertEquals(ReadinessState.WAITING, awaitReadiness { it.state == ReadinessState.WAITING }.readiness.state)
        statusAnswer = Readiness(ReadinessState.STALE, 2_820)
        val stale = awaitReadiness { it.state == ReadinessState.STALE }
        assertEquals(2_820, stale.readiness.ageSeconds)
        // Unlock is not touched by any of it: the state is still the one the Unlock button is on.
        assertEquals(machineA, stale.machine)
    }

    @Test
    fun theStatusIsNotAskedOnceTheReadyScreenIsLeftAndResumesWhenItIsBack() {
        store.saved = machineA
        statusAnswer = Readiness(ReadinessState.WAITING, 12)
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitReadiness { it.state == ReadinessState.WAITING }

        nextSession = { FakeSession() }
        onMain { flow.connect() }
        await<UnlockUiState.Connecting>()
        Thread.sleep(100)
        val calls = statusCalls.get()
        Thread.sleep(300)
        assertEquals("the status was asked for while connecting", calls, statusCalls.get())

        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
        val before = statusCalls.get()
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (statusCalls.get() <= before && System.nanoTime() < deadline) Thread.sleep(5)
        assertTrue("polling did not resume on the Ready screen", statusCalls.get() > before)
    }

    @Test
    fun theStatusIsNotAskedWhileTheAppIsInTheBackground() {
        store.saved = machineA
        statusAnswer = Readiness(ReadinessState.WAITING, 12)
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitReadiness { it.state == ReadinessState.WAITING }

        onMain { flow.movedToBackground() }
        Thread.sleep(100)
        val calls = statusCalls.get()
        Thread.sleep(300)
        assertEquals("asked while in the background", calls, statusCalls.get())

        onMain { flow.returnedToForeground() }
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (statusCalls.get() <= calls && System.nanoTime() < deadline) Thread.sleep(5)
        assertTrue("polling did not resume in the foreground", statusCalls.get() > calls)
    }

    private fun awaitCalls(atLeast: Int) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (statusCalls.get() < atLeast && System.nanoTime() < deadline) Thread.sleep(5)
        assertTrue("the status was asked ${statusCalls.get()} times, wanted $atLeast", statusCalls.get() >= atLeast)
    }

    @Test
    fun theReadyScreenAsksAtOnceAndThenOnlyAtItsPollInterval() {
        flow = newFlow(pollMillis = 60_000)
        store.saved = machineA
        statusAnswer = Readiness(ReadinessState.WAITING, 12)
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        Thread.sleep(300)
        assertEquals("asked again long before its poll interval", 1, statusCalls.get())
    }

    @Test
    fun comingBackToTheForegroundAsksAtOnceNotAtTheNextPoll() {
        flow = newFlow(pollMillis = 60_000)
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        onMain { flow.movedToBackground() }
        Thread.sleep(100)
        onMain { flow.returnedToForeground() }
        awaitCalls(2)
    }

    @Test
    fun checkingAgainByHandAsksAtOnceButNotMoreOftenThanTheMinimumGap() {
        flow = newFlow(pollMillis = 60_000, minGapMillis = 400)
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        onMain { flow.checkAgain() }
        onMain { flow.checkAgain() }
        Thread.sleep(150)
        assertEquals("a tap within the gap asked again", 1, statusCalls.get())
        Thread.sleep(400)
        onMain { flow.checkAgain() }
        awaitCalls(2)
        Thread.sleep(150)
        assertEquals("two taps made two checks", 2, statusCalls.get())
    }

    @Test
    fun noMoreThanOneCheckIsInFlight() {
        flow = newFlow(pollMillis = 60_000)
        val release = CountDownLatch(1)
        statusWaitsFor = release
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        repeat(5) { onMain { flow.checkAgain() } }
        Thread.sleep(300)
        assertEquals("a second check started while one was in flight", 1, statusCalls.get())
        release.countDown()
        // The taps made while it ran are answered by it, not by a second check straight after.
        Thread.sleep(300)
        assertEquals("a tap made during a check started another when it ended", 1, statusCalls.get())
    }

    @Test
    fun aRateLimitedAnswerKeepsTheLastKnownStateAndSilencesTheChecksForItsWait() {
        flow = newFlow(pollMillis = 20)
        store.saved = machineA
        statusAnswer = Readiness(ReadinessState.WAITING, 12)
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitReadiness { it.state == ReadinessState.WAITING }

        statusRateLimitedMillis = 600
        val before = statusCalls.get()
        awaitCalls(before + 1)
        val limitedAt = System.nanoTime()
        Thread.sleep(50) // the call that was told to wait has returned
        statusRateLimitedMillis = 0
        statusAnswer = Readiness(ReadinessState.ABSENT)
        // Neither the poll, nor the foreground, nor a tap gets through until the wait is up.
        onMain { flow.movedToBackground() }
        onMain { flow.returnedToForeground() }
        onMain { flow.checkAgain() }
        val limitedCalls = statusCalls.get()
        Thread.sleep(400)
        assertEquals("asked while it had been told to wait", limitedCalls, statusCalls.get())
        assertEquals(
            "the last known state was dropped for a 429",
            ReadinessState.WAITING,
            (flow.state.value as UnlockUiState.Ready).readiness.state,
        )
        // And once it is up, the chip recovers.
        awaitReadiness { it.state == ReadinessState.ABSENT }
        val waited = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - limitedAt)
        assertTrue("asked again after only $waited ms of 600", waited >= 500)
    }

    @Test
    fun aRateLimitedAnswerBeforeAnyStateLeavesTheChipChecking() {
        flow = newFlow(pollMillis = 20)
        store.saved = machineA
        statusRateLimitedMillis = 5_000
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        Thread.sleep(200)
        assertEquals(ReadinessState.CHECKING, (flow.state.value as UnlockUiState.Ready).readiness.state)
        assertEquals(1, statusCalls.get())
        // And it is not mistaken for something to quote after a failed dial.
        nextSession = { FakeSession(failWith = MachineFailure(FailureKind.UNREACHABLE, "", "mobile: gave up dialling", null)) }
        onMain { flow.connect() }
        val failed = await<UnlockUiState.Failed>()
        assertFalse(failed.technical.orEmpty(), failed.technical.orEmpty().contains("res:${R.string.failure_last_status}"))
    }

    // A failed dial says what discovery knows, in the headline: one lookup when the dial gives up,
    // and only for an unreachable machine.

    private val unreachable = MachineFailure(FailureKind.UNREACHABLE, "", "mobile: gave up dialling", null)

    private fun connectAndFail(failure: MachineFailure): UnlockUiState.Failed {
        nextSession = { FakeSession(failWith = failure) }
        onMain { flow.connect() }
        return await<UnlockUiState.Failed>()
    }

    private fun readyWithStatusShown(status: Readiness) {
        flow = newFlow(pollMillis = 60_000)
        store.saved = machineA
        statusAnswer = status
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitReadiness { it.state == status.state }
    }

    private fun age(seconds: Int) = ageLabel(seconds).let { "res:${it.res}(${it.args.joinToString(",")})" }

    @Test
    fun aDialThatFailedWhileTheMachineWasWaitingSaysItAnnouncedButDidNotAnswer() {
        readyWithStatusShown(Readiness(ReadinessState.WAITING, 42))
        val asked = statusCalls.get()
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable_waiting}(${age(42)})", failed.message)
        Thread.sleep(300)
        assertEquals("one lookup when the dial gave up", asked + 1, statusCalls.get())
    }

    @Test
    fun aDialThatFailedForAStaleRecordSaysWhenItLastAnnounced() {
        readyWithStatusShown(Readiness(ReadinessState.STALE, 47 * 60))
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable_stale}(${age(47 * 60)})", failed.message)
    }

    @Test
    fun aDialThatFailedForAMachineNeverAnnouncedSaysSo() {
        readyWithStatusShown(Readiness(ReadinessState.ABSENT))
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable_absent}", failed.message)
    }

    @Test
    fun theHeadlineIsTheLookupAtFailureTimeNotWhatTheChipSaidBefore() {
        readyWithStatusShown(Readiness(ReadinessState.WAITING, 30))
        statusAnswer = Readiness(ReadinessState.STALE, 10 * 60)
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable_stale}(${age(10 * 60)})", failed.message)
    }

    @Test
    fun withNoNetworkTheOldHeadlineStays() {
        readyWithStatusShown(Readiness(ReadinessState.NO_NETWORK))
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable}", failed.message)
    }

    @Test
    fun aRateLimitedLookupAtFailureTimeUsesTheLastKnownStateAndAsksNoMore() {
        readyWithStatusShown(Readiness(ReadinessState.STALE, 600))
        statusRateLimitedMillis = 60_000
        val asked = statusCalls.get()
        val failed = connectAndFail(unreachable)
        assertEquals(asked + 1, statusCalls.get())
        // Still the last known state: STALE, a little older than when it was seen.
        assertTrue(failed.message, failed.message.startsWith("res:${R.string.failure_unreachable_stale}("))
        // Told to wait, so a second failure does not ask again.
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        val again = connectAndFail(unreachable)
        assertEquals("looked up again while rate limited", asked + 1, statusCalls.get())
        assertTrue(again.message, again.message.startsWith("res:${R.string.failure_unreachable_stale}("))
    }

    @Test
    fun theLastKnownStateIsAsOldAsItHasGrownWhenItIsUsedInsteadOfALookup() {
        readyWithStatusShown(Readiness(ReadinessState.WAITING, 100))
        statusRateLimitedMillis = 3_600_000
        // Seen waiting at 100 s old; 20 minutes later it is a record 21 minutes old, not a waiting one.
        clockOffsetMillis.addAndGet(20 * 60_000L)
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable_stale}(${age(100 + 20 * 60)})", failed.message)
    }

    @Test
    fun aRateLimitedLookupWithNothingKnownKeepsTheOldHeadline() {
        flow = newFlow(pollMillis = 60_000)
        store.saved = machineA
        statusRateLimitedMillis = 60_000
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        awaitCalls(1)
        val failed = connectAndFail(unreachable)
        assertEquals("res:${R.string.failure_unreachable}", failed.message)
    }

    @Test
    fun otherFailuresDoNotLookAnythingUp() {
        readyWithStatusShown(Readiness(ReadinessState.WAITING, 42))
        val asked = statusCalls.get()
        val failed = connectAndFail(MachineFailure(FailureKind.HUNG_UP, "", "the machine hung up", null))
        assertEquals("res:${R.string.failure_hung_up}", failed.message)
        Thread.sleep(300)
        assertEquals("a lookup for a failure that is not about reaching the machine", asked, statusCalls.get())
    }

    /** The last status goes in the technical details of an unreachable failure, as well as the headline. */
    @Test
    fun anUnreachableMachineHasItsLastKnownStatusInTheTechnicalDetails() {
        readyWithStatusShown(Readiness(ReadinessState.STALE, 47 * 60))
        val failed = connectAndFail(unreachable)
        val technical = failed.technical.orEmpty()
        assertTrue(technical, technical.contains("res:${R.string.failure_last_status}"))
        assertTrue(technical, technical.contains("47"))
    }

    // The log: what the flow does is followable from logcat, and none of it is a secret.

    @Test
    fun aPairingIsLoggedWithoutItsCodeOrItsKeys() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB, code = "SECRET-CODE-123")) }
        await<UnlockUiState.Pairing>()
        val pairing = pairings.awaitOne()
        pairing.started.await(5, TimeUnit.SECONDS)
        pairing.finish()
        await<UnlockUiState.Ready>()

        val all = synchronized(logLines) { logLines.toList() }
        assertTrue("nothing was logged", all.any { it.contains("pair", ignoreCase = true) })
        assertTrue("the machine id was not logged", all.any { it.contains(machineB.deviceId.substringBefore('-')) })
        for (line in all) {
            assertFalse("the pairing code was logged: $line", line.contains("SECRET-CODE-123"))
            assertFalse("a pairing link was logged: $line", line.contains("psk="))
            assertFalse("the whole link was logged: $line", line.contains("synctang://pair"))
            assertFalse("the public key was logged: $line", line.contains("deadbeefcafe"))
        }
        assertTrue("whether there was a code is worth saying", all.any { it.contains("code=yes") || it.contains("has_code=true") })
    }

    @Test
    fun anUnlockIsLoggedWithoutTheKeyBytes() {
        val session = connectToARequest()
        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        await<UnlockUiState.Done>()
        assertTrue(session.answered)

        val all = synchronized(logLines) { logLines.toList() }
        // What the fake unwrapping returns is 32 bytes of 0x00; these are the other things a
        // careless log might include.
        for (line in all) {
            assertFalse("the key bytes were logged: $line", line.contains("[B@") || line.contains("0, 0, 0, 0"))
            assertFalse(line, line.contains("deadbeefcafe"))
        }
        val joined = all.joinToString("\n")
        for (want in listOf("Approve", "prompt", "unwrap", "Done")) {
            assertTrue("the log does not mention $want:\n$joined", joined.contains(want, ignoreCase = true))
        }
    }

    @Test
    fun everyStateChangeAndEveryUserActionIsLogged() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true) }
        onMain { flow.connect() }
        await<UnlockUiState.Approve>()
        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()

        val joined = synchronized(logLines) { logLines.toList() }.joinToString("\n")
        for (want in listOf("state Ready", "state Connecting", "state Approve", "action connect", "action cancel", "window armed", machineA.name)) {
            assertTrue("the log does not contain \"$want\":\n$joined", joined.contains(want))
        }
    }

    // Progress: the busy screens say what the dial, the pairing and the answer are doing,
    // read from the Go side while they run, and stop reading when they are over.

    private inline fun <reified T : UnlockUiState> awaitState(crossinline condition: (T) -> Boolean): T {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            val now = flow.state.value
            if (now is T && condition(now)) return now
            Thread.sleep(5)
        }
        fail("never matched, still ${flow.state.value}")
        throw AssertionError()
    }

    @Test
    fun theConnectingScreenFollowsWhatTheDialIsDoing() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.connect() }
        await<UnlockUiState.Connecting>()
        val session = sessions.awaitOne()

        session.shown = Progress(Stage.LOOKING_UP, attempt = 1)
        assertEquals(Stage.LOOKING_UP, awaitState<UnlockUiState.Connecting> { it.progress.stage == Stage.LOOKING_UP }.progress.stage)

        session.shown = Progress(Stage.CONNECTING, attempt = 2, route = Route.RELAY)
        val now = awaitState<UnlockUiState.Connecting> { it.progress.stage == Stage.CONNECTING }
        assertEquals(2, now.progress.attempt)
        assertEquals(Route.RELAY, now.progress.route)
        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
    }

    @Test
    fun theTimeSpentIsKeptFromTheStartAndNotRestartedByEachUpdate() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.connect() }
        val first = await<UnlockUiState.Connecting>().progress.startedAtMillis
        val session = sessions.awaitOne()
        session.shown = Progress(Stage.CHECKING_IDENTITY, attempt = 1)
        val later = awaitState<UnlockUiState.Connecting> { it.progress.stage == Stage.CHECKING_IDENTITY }
        assertEquals(first, later.progress.startedAtMillis)
        onMain { flow.cancel() }
    }

    @Test
    fun thePairingScreenSaysItIsWaitingForTheOperator() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.Pairing>()
        val pairing = pairings.awaitOne()
        pairing.started.await(5, TimeUnit.SECONDS)

        pairing.shown = Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1)
        awaitState<UnlockUiState.Pairing> { it.progress.stage == Stage.WAITING_FOR_OPERATOR }
        pairing.finish()
        await<UnlockUiState.Ready>()
    }

    // The code that the person compares with the one the machine shows: the pairing code only proves that
    // whoever answered had it, so while the operator decides the phone has to say which code is its own.
    @Test
    fun thePairingScreenCarriesTheCodeToCompareWhileTheOperatorDecides() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.Pairing>()
        val pairing = pairings.awaitOne()
        pairing.started.await(5, TimeUnit.SECONDS)
        assertEquals("", (await<UnlockUiState.Pairing>()).progress.shortCode)

        pairing.shown = Progress(Stage.WAITING_FOR_OPERATOR, attempt = 1, shortCode = "483 291")
        val shown = awaitState<UnlockUiState.Pairing> { it.progress.shortCode == "483 291" }
        assertEquals(Stage.WAITING_FOR_OPERATOR, shown.progress.stage)
        pairing.finish()
        await<UnlockUiState.Ready>()
    }

    @Test
    fun theWorkingScreenFollowsTheAnswer() {
        val session = connectToARequest()
        val release = CountDownLatch(1)
        onMain {
            flow.approve { _, _, _ ->
                // Off the main thread, which the poller needs to run on while the prompt is up.
                withContext(Dispatchers.IO) { release.await() }
                AuthOutcome.SUCCEEDED
            }
        }
        await<UnlockUiState.Working>()
        session.shown = Progress(Stage.WAITING_FOR_VERDICT, attempt = 1)
        awaitState<UnlockUiState.Working> { it.progress.stage == Stage.WAITING_FOR_VERDICT }
        release.countDown()
        await<UnlockUiState.Done>()
    }

    /** Nothing keeps asking once the screen is no longer a busy one. */
    @Test
    fun nothingIsPolledAfterTheBusyStateIsLeft() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.connect() }
        await<UnlockUiState.Connecting>()
        val session = sessions.awaitOne()
        awaitCalls(session) { it > 2 } // it is being polled while busy

        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
        Thread.sleep(100) // a poll in flight at the moment of leaving may still land
        val calls = session.progressCalls.get()
        Thread.sleep(300) // many intervals
        assertEquals("the session was polled after the screen left the busy state", calls, session.progressCalls.get())
    }

    @Test
    fun nothingIsPolledAfterAPairingEnds() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.Pairing>()
        val pairing = pairings.awaitOne()
        pairing.started.await(5, TimeUnit.SECONDS)
        pairing.finish()
        await<UnlockUiState.Ready>()
        Thread.sleep(100)
        val calls = pairing.progressCalls.get()
        Thread.sleep(300)
        assertEquals(calls, pairing.progressCalls.get())
    }

    private fun awaitCalls(session: FakeSession, condition: (Int) -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (System.nanoTime() < deadline) {
            if (condition(session.progressCalls.get())) return
            Thread.sleep(5)
        }
        fail("never polled enough: ${session.progressCalls.get()}")
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
        assertTrue("the session was never closed", awaitClosed(session))
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
        assertTrue("the session was never closed", awaitClosed(session))
    }

    @Test
    fun aDialThatFailsOffersToTryAgain() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = {
            FakeSession(failWith = MachineFailure(FailureKind.UNREACHABLE, "", "mobile: gave up dialling", null))
        }

        onMain { flow.connect() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals(Retry.REFRESH, failed.retry)
        assertEquals("res:${R.string.failure_unreachable}", failed.message)
        assertNull(failed.detail)
        assertEquals("mobile: gave up dialling", failed.technical)
    }

    @Test
    fun anUnexpectedFailureKeepsItsRawTextAndIsLogged() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        val error = Exception("mobile: something new")
        nextSession = { FakeSession(failWith = error) }

        onMain { flow.connect() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_other}", failed.message)
        assertNull(failed.detail)
        assertEquals("mobile: something new", failed.technical)
        // By text: coroutines may rethrow a copy across withContext.
        assertEquals(listOf(error.message), logged.map { it.message })
    }

    @Test
    fun aFailedPairingIsLoggedOnce() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        val error = Exception("mobile: gave up pairing")
        nextPairing = { FakePairing(failWith = error) }

        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.Failed>()
        assertEquals(listOf(error.message), logged.map { it.message })
    }

    @Test
    fun aFailedPairingIsExplained() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        nextPairing = {
            FakePairing(failWith = MachineFailure(FailureKind.PAIR_CODE_MISMATCH, "", "raw", null))
        }

        onMain { flow.pair(payload(machineB)) }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_pair_code_mismatch}", failed.message)
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
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

    // Decline and back on a waiting machine (defect 3)

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
        assertTrue("the session was never closed", awaitClosed(session))
    }

    @Test
    fun aLockedOutBiometricDeclinesAndSaysSo() {
        val session = connectToARequest()

        onMain { flow.approve { _, _, _ -> AuthOutcome.LOCKED_OUT } }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_biometric_lockout}", failed.message)
        assertEquals(1, session.declines.size)
        assertTrue("the session was never closed", awaitClosed(session))
    }

    /**
     * Adding a fingerprint or face retires the key for good (it is
     * created with setInvalidatedByBiometricEnrollment), and the
     * machine's enrolment names that key. The phone then needs a new
     * key and a new transport identity: the machine answers the first
     * recipient whose transport identity matches, so pairing again
     * under the old one would leave the dead key in front of the new.
     */
    @Test
    fun anInvalidatedKeyFoundBeforeThePromptDeclinesStartsAFreshIdentityAndSaysWhatToRevoke() {
        val session = connectToARequest()
        prepareFails = KeyInvalidated(RuntimeException("Key permanently invalidated"))

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_invalidated}(OLD-PHONE-ID)", failed.message)
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals(1, identityResets)
        assertNull(store.saved)
        assertEquals(1, session.declines.size)
        assertFalse(session.answered)
        assertTrue("the session was never closed", awaitClosed(session))
    }

    /**
     * The hardware refusing a fingerprint it was just given (after a successful
     * prompt) is an invalidation or a platform fault, and nothing here can tell which.
     * The machine is told no, the key is kept, and the next attempt, which starts an
     * operation before any prompt, finds out for certain.
     */
    @Test
    fun aKeyThatRefusesTheFingerprintItWasGivenDeclinesAndKeepsTheKey() {
        val session = connectToARequest()
        unwrapFails = KeyRefusedFingerprint(RuntimeException("Key user not authenticated"))

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_refused_fingerprint}", failed.message)
        assertEquals(1, session.declines.size)
        assertFalse(session.answered)
        assertTrue("the session was never closed", awaitClosed(session))
        assertEquals("the key is kept", 0, identityResets)
    }

    // A key that was invalidated while the app was closed is found when it starts,
    // not at the first Approve, so a dead public point is not offered for enrolment.

    @Test
    fun anInvalidatedKeyIsFoundAtStartAndTheIdentityStartedAfresh() {
        store.saved = machineA
        keyCheckFails = KeyInvalidated(RuntimeException("Key permanently invalidated"))
        onMain { flow.refresh() }

        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_invalidated}(OLD-PHONE-ID)", failed.message)
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals(1, identityResets)
    }

    @Test
    fun theKeyIsCheckedOnceNotOnEveryReturnToTheHomeScreen() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        onMain { flow.startPairing() }
        await<UnlockUiState.NeedsPairing>()
        Thread.sleep(300)
        assertEquals(1, keyChecks.get())
    }

    /**
     * Coming back to the app is when someone may have enrolled a finger in Settings,
     * which retires the key; the check runs again there, and only there.
     */
    @Test
    fun theKeyIsCheckedAgainWhenTheAppComesBackToTheForeground() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        Thread.sleep(300)
        assertEquals(1, keyChecks.get())

        keyCheckFails = KeyInvalidated(RuntimeException("Key permanently invalidated"))
        onMain { flow.returnedToForeground() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_invalidated}(OLD-PHONE-ID)", failed.message)
        assertEquals(2, keyChecks.get())
    }

    @Test
    fun comingBackWhileARequestIsWaitingChecksNothing() {
        connectToARequest()
        val before = keyChecks.get()
        onMain { flow.returnedToForeground() }
        Thread.sleep(300)
        assertEquals(before, keyChecks.get())
        assertTrue(flow.state.value is UnlockUiState.Approve)
    }

    /**
     * A dial the person starts while the start-up check is still running has its
     * session; replacing the identity under it would only make the machine refuse
     * an unknown peer, and overwrite the message. The approval finds the dead key.
     */
    @Test
    fun aCheckThatFindsAnInvalidKeyAfterTheDialHasStartedLeavesItAlone() {
        store.saved = machineA
        val hold = CountDownLatch(1)
        keyCheckWaitsFor = hold
        keyCheckFails = KeyInvalidated(RuntimeException("Key permanently invalidated"))
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true) }
        onMain { flow.connect() }
        await<UnlockUiState.Approve>()

        hold.countDown()
        Thread.sleep(500)
        assertTrue("the check replaced the screen: ${flow.state.value}", flow.state.value is UnlockUiState.Approve)
        assertEquals(0, identityResets)
    }

    @Test
    fun aKeyThatIsNotWholeWhilePairingAsksInsteadOfFailingGenerically() {
        identityFails = KeyNeedsReplacing(KeyProblem.HARDWARE_HALF_MISSING)
        onMain { flow.refresh() }
        await<UnlockUiState.KeyNeedsReplacing>()
        identityFails = KeyNeedsReplacing(KeyProblem.FILES_DAMAGED)

        onMain { flow.pair(payload(machineB)) }
        assertEquals(KeyProblem.FILES_DAMAGED, await<UnlockUiState.KeyNeedsReplacing>().problem)
        assertEquals(0, identityResets)
    }

    @Test
    fun aRefusedSoftwareKeyWhilePairingStopsAtItsOwnScreen() {
        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
        identityFails = SoftwareKeyRefused()

        onMain { flow.pair(payload(machineB)) }
        await<UnlockUiState.SoftwareKeyRefused>()
    }

    @Test
    fun aCheckThatFailsForAnyOtherReasonLeavesTheScreenAlone() {
        store.saved = machineA
        keyCheckFails = IllegalStateException("the Keystore did not answer")
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        Thread.sleep(300)
        assertTrue(flow.state.value is UnlockUiState.Ready)
        assertEquals(0, identityResets)
    }

    /**
     * A key stuck inside the Keystore (BACKEND_BUSY, seen on a Pixel 3a XL)
     * must end in a message, not a spinner, and the machine must be told so
     * that it does not wait for its own timeout.
     */
    @Test
    fun aKeyThatDoesNotRespondDeclinesAndSaysSo() {
        val session = connectToARequest()
        unwrapFails = KeyUseTimedOut(20)

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_timed_out}", failed.message)
        assertEquals(1, session.declines.size)
        assertFalse(session.answered)
        assertTrue("the session was never closed", awaitClosed(session))
        assertEquals("the key is kept: nothing says it is broken", 0, identityResets)
    }

    /**
     * Starting the operation is the Keystore call that is known to hang, so it
     * is bounded like the unwrapping is: past the limit the machine is told
     * no and the person is told why, instead of a Working screen with no way
     * out.
     */
    @Test
    fun aKeyThatDoesNotStartDeclinesAndSaysSo() {
        val session = connectToARequest()
        val stuck = CountDownLatch(1)
        prepareBlocksOn = stuck

        try {
            onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
            val failed = await<UnlockUiState.Failed>()
            assertEquals("res:${R.string.failure_key_timed_out}", failed.message)
            assertEquals(1, session.declines.size)
            assertFalse(session.answered)
            assertTrue("the session was never closed", awaitClosed(session))
            assertEquals("the key is kept: nothing says it is broken", 0, identityResets)
        } finally {
            stuck.countDown()
        }
    }

    /** A second press, queued behind the first, must not start a second prompt. */
    @Test
    fun aSecondApproveWhileOneIsUnderWayIsIgnored() {
        connectToARequest()
        val prompts = AtomicInteger()
        val release = CountDownLatch(1)
        val gate = BiometricGate { _, _, _ ->
            prompts.incrementAndGet()
            release.await()
            AuthOutcome.SUCCEEDED
        }

        onMain {
            flow.approve(gate)
            flow.approve(gate)
        }
        await<UnlockUiState.Working>()
        Thread.sleep(300)
        release.countDown()
        await<UnlockUiState.Done>()

        assertEquals(1, prompts.get())
        assertEquals(1, preparedCiphers.size)
    }

    // The machine's session has been running since the phone connected, so the window to
    // answer is counted from then and not from when the Approve screen appeared: a dial
    // that took a long time leaves correspondingly less. (The window in these tests is
    // 1500 ms; see approveWindowMillis in the flow.)

    @Test
    fun theApproveWindowIsCountedFromTheConnectionNotFromTheScreen() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        // The connection was made 800 ms before Connect returned, because the machine was
        // slow to send its request.
        nextSession = { FakeSession(connectReturnsAtOnce = true, connectedForMillis = 800) }
        onMain { flow.connect() }
        val approve = await<UnlockUiState.Approve>()
        val appeared = System.currentTimeMillis()

        // 1500 ms from the connection, which is 700 ms from now: not 1500 ms from now.
        val left = approve.expiresAtMillis - appeared
        assertTrue("$left ms left when the screen appeared", left in 400L..900L)
        val failed = await<UnlockUiState.Failed>()
        val gaveUp = System.currentTimeMillis()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        assertTrue("gave up ${gaveUp - appeared} ms after the screen appeared; the window is 1500 ms from the connection", gaveUp - appeared < 1_100)
    }

    @Test
    fun aDialThatTookLongerThanTheWindowLeavesNoTimeAtAll() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true, connectedForMillis = 1_800) }
        onMain { flow.connect() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        val session = sessions.single()
        assertEquals(listOf(APPROVAL_TIMED_OUT), session.declines.toList())
        assertTrue("the session was never closed", awaitClosed(session))
    }

    // The window to answer does not end when Approve is tapped: the fingerprint prompt that
    // follows is part of it. A prompt left up past the machine's patience could only produce
    // an answer to a session that has ended, so when the window runs out the prompt is
    // cancelled, the machine told no and the session closed, as for an Approve left alone.
    // (The window in these tests is 1500 ms from the connection.)

    /** A gate that never returns, and records that its coroutine was cancelled. */
    private class NeverGate : BiometricGate {
        val cancelled = CountDownLatch(1)
        val shown = CountDownLatch(1)
        override suspend fun authenticate(title: String, subtitle: String, cipher: Cipher): AuthOutcome =
            kotlinx.coroutines.suspendCancellableCoroutine { cont ->
                cont.invokeOnCancellation { cancelled.countDown() }
                shown.countDown()
            }
    }

    @Test
    fun theWindowRunningOutWhileThePromptIsUpCancelsItAndDeclinesTheMachineOnce() {
        val session = connectToARequest()
        val gate = NeverGate()
        onMain { flow.approve(gate) }
        await<UnlockUiState.Working>()
        assertTrue("the prompt was never shown", gate.shown.await(2, TimeUnit.SECONDS))

        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        assertTrue("the prompt was not cancelled", gate.cancelled.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(APPROVAL_TIMED_OUT), session.declines.toList())
        assertTrue("the session was never closed", awaitClosed(session))
        assertFalse(session.answered)
        Thread.sleep(300)
        assertEquals("declined more than once", 1, session.declines.size)
        assertTrue(flow.state.value is UnlockUiState.Failed)
    }

    @Test
    fun anApprovalJustBeforeTheWindowEndsAnswersAndNoExpiryFollows() {
        val session = connectToARequest()
        onMain {
            flow.approve { _, _, _ ->
                kotlinx.coroutines.delay(1_200)
                AuthOutcome.SUCCEEDED
            }
        }
        await<UnlockUiState.Done>()
        Thread.sleep(600) // past the window
        assertTrue(flow.state.value is UnlockUiState.Done)
        assertTrue(session.answered)
        assertTrue("the machine was told no after it was answered", session.declines.isEmpty())
    }

    /** The touch arrives after the window ended, from a prompt that did not notice being cancelled. */
    @Test
    fun aTouchAfterTheWindowEndedSendsNoAnswer() {
        val session = connectToARequest()
        onMain {
            flow.approve { _, _, _ ->
                // Not cancellable: this stands for a sensor match that lands after the expiry.
                withContext(kotlinx.coroutines.NonCancellable) { kotlinx.coroutines.delay(1_900) }
                AuthOutcome.SUCCEEDED
            }
        }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        Thread.sleep(1_200) // the late success has come and gone
        assertFalse("an answer was sent after the machine was declined", session.answered)
        assertEquals(listOf(APPROVAL_TIMED_OUT), session.declines.toList())
        assertTrue(flow.state.value is UnlockUiState.Failed)
    }

    /** An answer already being sent is not cut off by the window running out under it. */
    @Test
    fun anAnswerThatIsBeingSentIsNotInterruptedByTheWindowEnding() {
        val session = connectToARequest()
        unwrapTakesMillis = 600
        onMain {
            flow.approve { _, _, _ ->
                kotlinx.coroutines.delay(1_100)
                AuthOutcome.SUCCEEDED
            }
        }
        // The window ends at 1500 ms, in the middle of the 600 ms the unwrapping takes.
        await<UnlockUiState.Done>()
        assertTrue(session.answered)
        assertTrue("the machine was told no while it was being answered", session.declines.isEmpty())
    }

    // Cancelling the fingerprint prompt does not end the request: the machine is still
    // waiting and the window is still running, so the Approve screen comes back, with the
    // machine not told no, and the person can try again or decline.
    @Test
    fun cancellingThePromptBringsTheApproveScreenBackAndTellsTheMachineNothing() {
        val session = connectToARequest()
        onMain { flow.approve { _, _, _ -> AuthOutcome.CANCELLED } }
        val back = await<UnlockUiState.Approve>()
        assertTrue("the window must still be running", back.expiresAtMillis > 0)
        assertTrue("the machine was told no: ${session.declines}", session.declines.isEmpty())
        assertFalse(session.closed)
    }

    @Test
    fun aCancelledPromptCanBeTriedAgainWithinTheWindow() {
        val session = connectToARequest()
        onMain { flow.approve { _, _, _ -> AuthOutcome.CANCELLED } }
        await<UnlockUiState.Approve>()

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        await<UnlockUiState.Done>()
        assertTrue(session.answered)
        assertTrue(session.declines.isEmpty())
    }

    @Test
    fun aCancelledPromptLeftAloneStillRunsOutOfTime() {
        val session = connectToARequest()
        onMain { flow.approve { _, _, _ -> AuthOutcome.CANCELLED } }
        await<UnlockUiState.Approve>()

        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        assertEquals(listOf(APPROVAL_TIMED_OUT), session.declines.toList())
    }

    // One tap: Unlock dials, and when the machine's request arrives the fingerprint
    // prompt comes up by itself, naming the machine, with no Approve screen in between.

    private fun unlockInOneTap(gate: BiometricGate): FakeSession {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true) }
        onMain { flow.connect(gate) }
        return sessions.awaitOne()
    }

    @Test
    fun unlockingWithAGatePromptsAsSoonAsTheRequestArrives() {
        val prompts = Collections.synchronizedList(mutableListOf<String>())
        val session = unlockInOneTap { _, subtitle, _ ->
            prompts += subtitle
            AuthOutcome.SUCCEEDED
        }

        await<UnlockUiState.Done>()
        assertTrue(session.answered)
        assertTrue(session.declines.isEmpty())
        assertEquals(1, prompts.size)
        assertEquals("the prompt must name the machine", "res:${R.string.biometric_subtitle}(${machineA.name})", prompts.single())
    }

    @Test
    fun theOneTapPromptIsNeverShownForAMachineThatAskedNothing() {
        val prompts = AtomicInteger()
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        nextSession = { FakeSession(connectReturnsAtOnce = true, hasRequest = false) }
        onMain { flow.connect { _, _, _ -> prompts.incrementAndGet(); AuthOutcome.SUCCEEDED } }

        await<UnlockUiState.Failed>()
        assertEquals(0, prompts.get())
    }

    @Test
    fun aCancelledOneTapPromptLandsOnTheApproveScreen() {
        val session = unlockInOneTap { _, _, _ -> AuthOutcome.CANCELLED }

        await<UnlockUiState.Approve>()
        assertTrue(session.declines.isEmpty())
    }

    @Test
    fun aLockedOutOneTapPromptDeclinesAndSaysSo() {
        val session = unlockInOneTap { _, _, _ -> AuthOutcome.LOCKED_OUT }

        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_biometric_lockout}", failed.message)
        assertEquals(listOf(KEY_HOLDER_NOT_AUTHENTICATED), session.declines.toList())
    }

    @Test
    fun connectingWithoutAGateStillStopsOnTheApproveScreen() {
        connectToARequest()
        Thread.sleep(200)
        assertTrue(flow.state.value is UnlockUiState.Approve)
    }

    // An Approve screen cannot stay up for ever. The machine ended one session
    // about 100 seconds after asking (its own cap is three minutes), while the
    // screen sat there for seventeen minutes offering an Approve that could only
    // fail, so the app gives up on its own, tells the machine, and says so.

    @Test
    fun anApproveScreenLeftOpenGivesUpAndTellsTheMachine() {
        val session = connectToARequest()

        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.error_approval_expired}", failed.message)
        assertEquals(listOf(APPROVAL_TIMED_OUT), session.declines.toList())
        assertTrue("the session was never closed", awaitClosed(session))
        assertFalse(session.answered)
    }

    @Test
    fun approvingInTimeIsNotUndoneWhenTheWindowWouldHaveEnded() {
        connectToARequest()

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        await<UnlockUiState.Done>()
        Thread.sleep(2_000)
        assertTrue("the expiry fired after an answer: ${flow.state.value}", flow.state.value is UnlockUiState.Done)
    }

    @Test
    fun decliningInTimeIsNotFollowedByAnExpiry() {
        connectToARequest()

        onMain { flow.cancel() }
        await<UnlockUiState.Ready>()
        Thread.sleep(2_000)
        assertTrue("the expiry fired after a decline: ${flow.state.value}", flow.state.value is UnlockUiState.Ready)
    }

    // The fingerprint is bound to the one operation that unwraps the key: the
    // cipher the flow prepares is the one the prompt authenticates, and the one that
    // then unwraps. A prompt that authenticated some other cipher would prove nothing.
    @Test
    fun thePromptAuthenticatesTheCipherThatThenUnwrapsTheKey() {
        val session = connectToARequest()
        var seenByThePrompt: Cipher? = null

        onMain {
            flow.approve { _, _, cipher ->
                seenByThePrompt = cipher
                AuthOutcome.SUCCEEDED
            }
        }
        await<UnlockUiState.Done>()

        assertEquals(1, preparedCiphers.size)
        assertSame("the prompt was given a different cipher from the one prepared", preparedCiphers.single(), seenByThePrompt)
        assertSame("the key was unwrapped with a different cipher from the one authenticated", preparedCiphers.single(), unwrappedWith.single())
        assertTrue(session.answered)
    }

    // Declining the prompt must not unwrap anything.
    @Test
    fun aDeclinedPromptNeverUnwrapsTheKey() {
        val session = connectToARequest()

        onMain { flow.approve { _, _, _ -> AuthOutcome.CANCELLED } }
        await<UnlockUiState.Failed>()

        assertTrue("the key was unwrapped without authentication", unwrappedWith.isEmpty())
        assertFalse(session.answered)
    }

    // A key that was retired by a new fingerprint is found when the cipher is
    // prepared, before any prompt, so nobody is asked to touch the sensor for an
    // answer that cannot be given.
    @Test
    fun aKeyThatWasInvalidatedIsFoundBeforeThePromptAppears() {
        val session = connectToARequest()
        prepareFails = KeyInvalidated(RuntimeException("Key permanently invalidated"))
        var prompted = false

        onMain { flow.approve { _, _, _ -> prompted = true; AuthOutcome.SUCCEEDED } }
        val failed = await<UnlockUiState.Failed>()

        assertFalse("the person was asked for a fingerprint for a key that is gone", prompted)
        assertEquals("res:${R.string.failure_key_invalidated}(OLD-PHONE-ID)", failed.message)
        assertEquals(Retry.SCAN_AGAIN, failed.retry)
        assertEquals(1, identityResets)
        assertEquals(1, session.declines.size)
        assertFalse(session.answered)
        assertTrue("the session was never closed", awaitClosed(session))
    }

    @Test
    fun aFailureToPrepareTheKeyDeclinesAndSaysSo() {
        val session = connectToARequest()
        prepareFails = IllegalStateException("no key holder key under alias x")

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        val failed = await<UnlockUiState.Failed>()

        assertEquals("res:${R.string.failure_other}", failed.message)
        assertEquals(1, session.declines.size)
        assertEquals(0, identityResets)
        assertTrue(awaitClosed(session))
    }

    // A software-backed key (decided 2026-10-28: fail closed, with an opt-in)

    @Test
    fun aSoftwareKeyStopsAtAScreenThatSaysSoInsteadOfPairing() {
        identityFails = SoftwareKeyRefused()
        onMain { flow.refresh() }
        val refused = await<UnlockUiState.SoftwareKeyRefused>()
        assertEquals(BackAction.EXIT, backAction(refused))
        assertNull("About must not offer the identity of a refused key", com.github.muelli.synctang.ui.aboutIdentity(refused))
        assertEquals(0, softwareKeyAllowances)
    }

    @Test
    fun allowingASoftwareKeyRecordsTheChoiceAndCarriesOn() {
        identityFails = SoftwareKeyRefused()
        onMain { flow.refresh() }
        await<UnlockUiState.SoftwareKeyRefused>()

        onMain { flow.allowSoftwareKey() }
        await<UnlockUiState.NeedsPairing>()
        assertEquals(1, softwareKeyAllowances)
    }

    @Test
    fun anAlreadyPairedPhoneIsStoppedToo() {
        store.saved = machineA
        identityFails = SoftwareKeyRefused()
        onMain { flow.refresh() }
        await<UnlockUiState.SoftwareKeyRefused>()

        onMain { flow.allowSoftwareKey() }
        assertEquals(machineA, await<UnlockUiState.Ready>().machine)
    }

    // A key that is half there is the person's to decide about, never the app's

    /**
     * The wrapped scalar and the public point are two files. When they do not belong
     * together the Go side sends nothing, and the flow says so as the phone's own
     * problem, with the same choice as for any key that is not whole, rather than
     * telling someone the machine refused.
     */
    @Test
    fun aSavedKeyThatDoesNotMatchItsPublicPointAsksInsteadOfBlamingTheMachine() {
        store.saved = machineA
        onMain { flow.refresh() }
        await<UnlockUiState.Ready>()
        val mismatch = MachineFailure(FailureKind.KEY_MISMATCH, "", "mobile: saved key does not match", null)
        nextSession = { FakeSession(connectReturnsAtOnce = true, answerFailsWith = mismatch) }
        onMain { flow.connect() }
        await<UnlockUiState.Approve>()
        val session = sessions.single()

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
        val stopped = await<UnlockUiState.KeyNeedsReplacing>()
        assertEquals(KeyProblem.FILES_DO_NOT_MATCH, stopped.problem)
        assertEquals("the machine was told no", 1, session.declines.size)
        assertTrue(awaitClosed(session))
        assertEquals("nothing is deleted until the person says so", 0, identityResets)
        assertEquals(machineA, store.saved)
    }

    @Test
    fun aKeyWithOneHalfMissingStopsAtAScreenThatAsks() {
        store.saved = machineA
        identityFails = KeyNeedsReplacing(KeyProblem.HARDWARE_HALF_MISSING)
        onMain { flow.refresh() }
        val stopped = await<UnlockUiState.KeyNeedsReplacing>()
        assertEquals(KeyProblem.HARDWARE_HALF_MISSING, stopped.problem)
        assertEquals(BackAction.EXIT, backAction(stopped))
        assertNull(com.github.muelli.synctang.ui.aboutIdentity(stopped))
        assertEquals("nothing may be deleted until the person says so", 0, identityResets)
        assertEquals("the paired machine is kept", machineA, store.saved)
    }

    @Test
    fun tryingAgainAfterAPassingFaultCarriesOn() {
        store.saved = machineA
        identityFails = KeyNeedsReplacing(KeyProblem.HARDWARE_HALF_MISSING)
        onMain { flow.refresh() }
        await<UnlockUiState.KeyNeedsReplacing>()

        identityFails = null
        onMain { flow.refresh() }
        assertEquals(machineA, await<UnlockUiState.Ready>().machine)
        assertEquals(0, identityResets)
    }

    @Test
    fun makingANewKeyRetiresTheOldIdentityAndSaysWhichOne() {
        identityFails = KeyNeedsReplacing(KeyProblem.FILES_DAMAGED)
        onMain { flow.refresh() }
        await<UnlockUiState.KeyNeedsReplacing>()

        onMain { flow.replaceKey() }
        val failed = await<UnlockUiState.Failed>()
        assertEquals("res:${R.string.failure_key_replaced}(OLD-PHONE-ID)", failed.message)
        assertEquals(1, identityResets)
        assertEquals(Retry.REFRESH, failed.retry)

        onMain { flow.refresh() }
        await<UnlockUiState.NeedsPairing>()
    }

    @Test
    fun anApprovedRequestIsAnswered() {
        val session = connectToARequest()

        onMain { flow.approve { _, _, _ -> AuthOutcome.SUCCEEDED } }
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
    private val answerFailsWith: Exception? = null,
    private val connectedForMillis: Long = 0,
    private val connectTakesMillis: Long = 0,
    private val hasRequest: Boolean = true,
) : MachineSession {
    override fun connectedForMillis(): Long = connectedForMillis
    val started = CountDownLatch(1)
    private val release = CountDownLatch(1)
    @Volatile var cancelled = false
    @Volatile var closed = false
    @Volatile var answered = false
    val declines: MutableList<String> = Collections.synchronizedList(mutableListOf())

    override fun connect() {
        started.countDown()
        failWith?.let { throw it }
        if (connectTakesMillis > 0) Thread.sleep(connectTakesMillis)
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

    /** What the Go side would say it is doing; the test changes it. */
    @Volatile var shown = Progress()
    val progressCalls = java.util.concurrent.atomic.AtomicInteger()
    override fun progress(): Progress {
        progressCalls.incrementAndGet()
        return shown
    }

    override fun machineName() = "machine"
    override fun peerId() = "PEER"
    override fun hasRequest() = hasRequest
    override fun requestKidHex() = "ab"
    override fun answer(unwrap: () -> ByteArray) {
        unwrap()
        answerFailsWith?.let { throw it }
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

    @Volatile var machine: Machine? = null

    @Volatile var shown = Progress()
    val progressCalls = java.util.concurrent.atomic.AtomicInteger()
    override fun progress(): Progress {
        progressCalls.incrementAndGet()
        return shown
    }

    override fun pair(machine: Machine, code: String, identity: Identity) {
        this.machine = machine
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
