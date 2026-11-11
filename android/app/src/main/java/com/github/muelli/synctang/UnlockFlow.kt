// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes
import javax.crypto.Cipher
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * The outcome of a biometric prompt, as the unlock flow cares about it.
 */
enum class AuthOutcome {
    SUCCEEDED,
    CANCELLED,
    LOCKED_OUT,
    LOCKED_OUT_PERMANENTLY,
    NO_BIOMETRIC,
    UNAVAILABLE,
}

/**
 * A biometric prompt, abstracted so the unlock flow does not depend on
 * an Activity. [BiometricGateImpl] is the real one; a test can supply
 * its own.
 *
 * [cipher] is the one operation the fingerprint is for: the prompt
 * authenticates exactly this cipher, and only after it succeeds can the
 * cipher unwrap the key.
 */
fun interface BiometricGate {
    suspend fun authenticate(title: String, subtitle: String, cipher: Cipher): AuthOutcome
}

/** What a Failed screen's button does. */
enum class Retry {
    /** Back to whichever of the pairing and Unlock screens applies. */
    REFRESH,

    /** A pairing failed: its code is spent, so scan a fresh one. */
    SCAN_AGAIN,
}

/**
 * Everything the single Unlock screen can be showing.
 */
sealed interface UnlockUiState {
    /**
     * Scan a machine's QR code. [kept] are the machines already paired, which stay paired
     * whatever happens here; a pairing that works adds one. [forgotten] is a machine that has just
     * been forgotten, which the screen offers to take back.
     */
    data class NeedsPairing(
        val identity: Identity,
        val kept: List<Machine> = emptyList(),
        val forgotten: Machine? = null,
    ) : UnlockUiState

    /**
     * Paired with one machine and idle; the Unlock button is the only thing to do.
     * [forgotten] is the machine that has just been forgotten, which the screen offers to
     * take back.
     */
    data class Ready(
        val machine: Machine,
        val identity: Identity,
        val forgotten: Machine? = null,
        val readiness: Readiness = Readiness(),
    ) : UnlockUiState

    /**
     * Paired with two machines or more and idle: one row for each, the one used last first, each with
     * what discovery last said of it in [readiness] (a machine not in it has not been looked up yet).
     */
    data class Machines(
        val machines: List<Machine>,
        val identity: Identity,
        val readiness: Map<String, Readiness> = emptyMap(),
        val forgotten: Machine? = null,
    ) : UnlockUiState

    /** Dialling [machine]; [progress] says how far the dial has got. */
    data class Connecting(val machine: Machine, val progress: Progress = Progress()) : UnlockUiState

    /** Enrolling this phone on [machine] over the network. */
    data class Pairing(val machine: Machine, val progress: Progress = Progress()) : UnlockUiState

    /**
     * A recovery request has arrived and is waiting for approval. [expiresAtMillis] is
     * when the app gives up on it, counted from when the connection was made (see
     * [APPROVE_WINDOW_MILLIS]), on the flow's clock.
     */
    data class Approve(
        val machine: Machine,
        val peerId: String,
        val kidHex: String,
        val expiresAtMillis: Long = 0,
    ) : UnlockUiState

    /** Authenticating, computing the answer, or sending it. */
    data class Working(val machine: Machine, val progress: Progress = Progress()) : UnlockUiState

    data class Done(val machine: Machine) : UnlockUiState

    /**
     * Android made this phone's key in software, not in secure hardware,
     * and its owner has not accepted that. Nothing proceeds until they
     * do ([UnlockFlow.allowSoftwareKey]).
     */
    object SoftwareKeyRefused : UnlockUiState

    /**
     * The phone's key is not whole ([KeyHolderKey.ensureKey]), and what to do
     * about it is the person's call: try again, in case it was a passing fault,
     * or make a new key ([UnlockFlow.replaceKey]).
     */
    data class KeyNeedsReplacing(val problem: KeyProblem) : UnlockUiState

    /**
     * [message] is in words for a person; [detail], when there is one,
     * is the machine's own reason. [technical] is the raw error text,
     * shown only on request.
     */
    data class Failed(
        val machine: Machine?,
        val message: String,
        val retry: Retry = Retry.REFRESH,
        val detail: String? = null,
        val technical: String? = null,
    ) : UnlockUiState
}

/**
 * What this phone is, as the machine needs to hear it at enrolment
 * time: the transport identity it will dial from, and the long-term
 * public point the machine encrypts against.
 */
data class Identity(
    val deviceId: String,
    val publicKeyHex: String,
    val backing: KeyHolderKey.Backing,
)

// What the machine is told, and logs, when this phone will not answer.
// Protocol text for the machine's operator rather than anything shown
// on this phone, so not a string resource.
internal const val DECLINED_BY_KEY_HOLDER = "declined by the key holder"
internal const val KEY_HOLDER_NOT_AUTHENTICATED = "key holder did not authenticate"
internal const val APPROVAL_TIMED_OUT = "key holder did not answer in time"

/**
 * How long the app waits for the person to approve, counted from when the connection
 * to the machine was made (the machine's session and any relay's idle clock started
 * then), not from when the Approve screen appeared.
 *
 * Nothing is sent while the person decides, and the phone cannot send anything
 * without changing the protocol, so the connection is idle. Measured with the real
 * strelaysrv v1.30.0 on a private relay (integration/relay_idle_test.go), an idle
 * session closes with EOF on both sides 2 min 0 s after the last traffic with the
 * relay's defaults, which is the relay's network timeout and not its ping interval.
 * That is longer than what the real phone saw: three sessions ended with EOF about 90
 * to 100 seconds after connecting, through whatever path and relay that was, which the
 * experiment did not reproduce. The window is the shortest of those, 90 s, less a 15 s
 * margin, so that the app gives up and tells the machine, rather than offering an
 * Approve that can only fail. See docs/relay-idle.md.
 */
internal const val APPROVE_WINDOW_MILLIS = 75_000L

/** How often a busy screen asks how far the Go side has got: often enough to feel live, rare enough to cost nothing. */
internal const val POLL_INTERVAL_MILLIS = 500L

/** How many unknown parameter names a refusal lists, so a hostile link cannot fill the screen. */
private const val MAX_NAMES_SHOWN = 3

/** The most lookups, in one poll interval, that the readiness loop makes over the whole list of machines. */
private const val MAX_READINESS_LOOKUPS_PER_POLL = 3

/** The machines this phone is paired with. [PairingStore] is the real one. */
interface MachineStore {
    /** Every paired machine, the one used last first. */
    fun machines(): List<Machine>

    /** The machine used last, if there is one. */
    fun machine(): Machine? = machines().firstOrNull()

    /** The paired machine with this Device ID, if there is one. */
    fun machine(deviceId: String): Machine? = machines().firstOrNull { it.deviceId == deviceId }

    /** Adds the machine, or updates the one with its Device ID; it becomes the one used last. */
    fun saveMachine(machine: Machine)

    /** Removes the machine; nothing else is touched. */
    fun forget(deviceId: String)

    /** The machine with this Device ID is the one used last now. */
    fun markUsed(deviceId: String)
}

/**
 * One dial to a machine and the exchange that follows, as the unlock
 * flow uses it. The real one wraps the gomobile `Session`; see
 * [UnlockViewModel].
 */
interface MachineSession {
    /** Blocks until the machine has said what it wants, or throws. */
    fun connect()

    /**
     * How long ago the connection to the machine was made, in milliseconds, once [connect]
     * has returned. The machine's session, and a relay's idle clock, started then, which
     * is earlier than now if the machine was slow to send its request.
     */
    fun connectedForMillis(): Long = 0

    /** How far this session has got, read while [connect] or [answer] runs; never blocks. */
    fun progress(): Progress = Progress()

    /** Interrupts a [connect] running on another thread; returns at once. */
    fun cancel()

    fun close()
    fun decline(reason: String)
    fun machineName(): String
    fun peerId(): String
    fun hasRequest(): Boolean
    fun requestKidHex(): String

    /**
     * Answers the request with the key [unwrap] returns, and waits for
     * the machine's verdict. [unwrap] is called once, here, and what it
     * returns is wiped before this returns.
     */
    fun answer(unwrap: () -> ByteArray)
}

/** One network pairing. The real one wraps the gomobile `Pairer`. */
interface PairingAttempt {
    /** Blocks until the machine has enrolled this phone, or throws. */
    fun pair(machine: Machine, code: String, identity: Identity)

    /** How far the pairing has got, read while [pair] runs; never blocks. */
    fun progress(): Progress = Progress()

    /** Interrupts a [pair] running on another thread; returns at once. */
    fun cancel()
}

/** String resources, so the flow can be tested without a Context. */
interface Strings {
    fun get(@StringRes id: Int, vararg args: Any): String
}

/** What the system back gesture does on each screen. */
enum class BackAction {
    /** Leave the app, as back normally does. */
    EXIT,

    /** Exactly what Decline or Cancel on that screen does: [UnlockFlow.cancel]. */
    CANCEL,

    /** Leave the pairing screen for the machine already paired. */
    KEEP_CURRENT_MACHINE,

    /** Swallow it: an answer is on its way and cannot be taken back. */
    IGNORE,
}

/**
 * Back on a screen where the machine is waiting for this phone must
 * tell the machine, as Decline does, rather than finish the activity
 * and leave it waiting for its timeout.
 *
 * Working is swallowed rather than cancelled: it covers the moments
 * after the answer has been computed, when closing the connection
 * would only turn "unlocked" into "may or may not have unlocked".
 */
fun backAction(state: UnlockUiState): BackAction = when (state) {
    is UnlockUiState.NeedsPairing ->
        if (state.kept.isNotEmpty()) BackAction.KEEP_CURRENT_MACHINE else BackAction.EXIT
    is UnlockUiState.Ready, is UnlockUiState.Machines, is UnlockUiState.Done, is UnlockUiState.Failed,
    is UnlockUiState.SoftwareKeyRefused, is UnlockUiState.KeyNeedsReplacing -> BackAction.EXIT
    is UnlockUiState.Connecting, is UnlockUiState.Pairing,
    is UnlockUiState.Approve -> BackAction.CANCEL
    is UnlockUiState.Working -> BackAction.IGNORE
}

/**
 * What a scanned or typed pairing payload asks for.
 */
sealed interface PairingPlan {
    object Invalid : PairingPlan

    /** No one-time code: remember the machine; enrolment happens by hand. */
    data class Remember(val machine: Machine) : PairingPlan

    /** A one-time code: enrol over the network, then remember the machine. */
    data class Enrol(val machine: Machine, val code: String) : PairingPlan

    /** The link asks for something this app does not know: refuse, saying what. */
    data class UnknownParameters(val names: List<String>) : PairingPlan

    /** The link gives a setting more than once: refuse, saying which. */
    data class RepeatedParameters(val names: List<String>) : PairingPlan

    /** The link's [name] setting (discovery) is not a server URL. */
    data class BadSetting(val name: String) : PairingPlan

    /** The link's discovery server is plain http without `?insecure`. */
    object PlainHttpDiscovery : PairingPlan
}

fun planPairing(scanned: String): PairingPlan {
    val machine = PairingPayload.parse(scanned) ?: return PairingPlan.Invalid
    PairingPayload.unknownParameters(scanned).takeIf { it.isNotEmpty() }
        ?.let { return PairingPlan.UnknownParameters(it) }
    PairingPayload.repeatedParameters(scanned).takeIf { it.isNotEmpty() }
        ?.let { return PairingPlan.RepeatedParameters(it) }
    PairingPayload.badSetting(scanned)?.let { return PairingPlan.BadSetting(it) }
    if (PairingPayload.plainHttpDiscoveryWithoutInsecure(scanned)) return PairingPlan.PlainHttpDiscovery
    val code = PairingPayload.pairingCode(scanned) ?: return PairingPlan.Remember(machine)
    return PairingPlan.Enrol(machine, code)
}

/**
 * The unlock flow's state machine, kept free of Android so that it can
 * be tested on the JVM. [UnlockViewModel] supplies the real Keystore,
 * Go bridge and storage behind the interfaces above.
 *
 * Every public method is called on [scope]'s dispatcher (the main
 * thread in the app); blocking work goes to [io].
 */
class UnlockFlow(
    private val scope: CoroutineScope,
    private val io: CoroutineDispatcher,
    private val store: MachineStore,
    private val identity: () -> Identity,
    private val newSession: (Machine) -> MachineSession,
    private val newPairing: () -> PairingAttempt,
    // One use of the key: a cipher the fingerprint will be bound to, and
    // the unwrapping that cipher then does.
    private val prepareKey: () -> Cipher,
    private val unwrapKey: (Cipher) -> ByteArray,
    private val resetIdentity: () -> String,
    // Told after the set of paired machines changed, so that what is published of it (the launcher's
    // shortcuts) follows. Not called from the identity reset, which the caller knows about already.
    private val machinesChanged: () -> Unit = {},
    private val allowSoftwareKey: () -> Unit = {},
    private val approveWindowMillis: Long = APPROVE_WINDOW_MILLIS,
    // How long either Keystore call may take: a stuck one cannot be
    // interrupted, only left behind, so each runs on a thread of its own.
    private val keyUseTimeoutSeconds: Long = KEY_USE_TIMEOUT_SECONDS,
    // Starts and drops one operation with the key, which is how a key retired by a
    // change to the phone's biometrics is found out (it throws [KeyInvalidated]).
    // Run once per start, behind the home screen, so a key that died while the app
    // was closed is not offered for enrolment.
    private val checkKey: () -> Unit = {},
    // How the Ready screen finds out whether the machine looks like it is waiting: one lookup
    // on the discovery server, never a connection to the machine. Blocks; runs on [io].
    private val checkStatus: (Machine) -> StatusAnswer = { StatusAnswer.Known(Readiness()) },
    private val readinessPollMillis: Long = READINESS_POLL_MILLIS,
    private val readinessMinGapMillis: Long = READINESS_MIN_GAP_MILLIS,
    private val readinessJitter: () -> Double = { kotlin.random.Random.nextDouble() },
    // How often the busy screens ask the Go side how far it has got, and the clock their
    // elapsed time is counted on.
    private val pollIntervalMillis: Long = POLL_INTERVAL_MILLIS,
    private val clock: () -> Long = System::currentTimeMillis,
    private val strings: Strings,
    private val log: (String, Throwable) -> Unit = { _, _ -> },
    // Where the flow says what it is doing: every state change, every user action, the
    // approve window, the prompt, the key operations (never their bytes), and failures.
    private val appLog: AppLog = AppLog.silent,
) {
    /** The state, with every change written to the log. */
    private inner class LoggedState(initial: UnlockUiState) {
        private val flow = MutableStateFlow(initial)
        var value: UnlockUiState
            get() = flow.value
            set(next) {
                val before = flow.value
                flow.value = next
                if (before != next) appLog.d { "state " + describeForLog(next) }
                followReadiness(next)
                startWaitingUnlock(next)
            }

        fun asStateFlow(): StateFlow<UnlockUiState> = flow.asStateFlow()
    }

    // The status of the paired machine as last looked up, kept so that the Ready screen
    // comes back showing it and a failed dial can say what it was.
    private val lastReadiness = HashMap<String, Readiness>()
    private val lastReadinessAt = HashMap<String, Long>()
    private var readinessJob: Job? = null
    private var readinessMachines: List<String> = emptyList()
    // A machine on the list that was asked about by name (Check again on its row), for the next look.
    private var nudgeTarget: String? = null
    @Volatile private var appForeground = true

    private val _state = LoggedState(UnlockUiState.Working(Machine("", "")))

    /** The app has left the foreground: stop asking about the machine until it is back. */
    fun movedToBackground() {
        appLog.d { "action moved to background" }
        appForeground = false
    }

    // When something other than the timer wants a check now: the app coming back, a tap.
    // Conflated, so that any number of them while a check runs is one.
    private val readinessNudges = Channel<Unit>(Channel.CONFLATED)

    // Until when the discovery server has told us to leave it alone, by its address.
    private val rateLimitedUntil = HashMap<String, Long>()

    /**
     * Starts asking about the machine while the Ready screen is showing, and stops the
     * moment the state is anything else. Asks at once, then every [readinessPollMillis] give
     * or take 20 percent, not at all while the app is in the background, and one lookup at a
     * time. Each answer goes into the Ready state (which never changes what Unlock does), is
     * kept for a failed dial to quote, and is written to the log.
     *
     * Something that is not the timer (the app coming back to the foreground, a tap on Check
     * again) asks at once too, but never within [readinessMinGapMillis] of the last check.
     * And when the server answers that it is being asked too often, nothing asks again until
     * its Retry-After is up, while the chip goes on showing what it showed.
     */
    private fun followedMachines(state: UnlockUiState): List<Machine> = when (state) {
        is UnlockUiState.Ready -> listOf(state.machine)
        is UnlockUiState.Machines -> state.machines
        else -> emptyList()
    }

    private fun followReadiness(state: UnlockUiState) {
        val machines = followedMachines(state)
        if (machines.isEmpty()) {
            readinessJob?.cancel()
            readinessJob = null
            readinessMachines = emptyList()
            return
        }
        val ids = machines.map { it.deviceId }
        if (readinessJob?.isActive == true && readinessMachines == ids) return
        readinessJob?.cancel()
        readinessMachines = ids
        readinessJob = scope.launch {
            while (readinessNudges.tryReceive().isSuccess) { /* asked for before this screen was up */ }
            var scheduled = true
            var lastStart = Long.MIN_VALUE / 2
            var turn = 0
            var machine = machines.first()
            while (true) {
                val shown = followedMachines(_state.value)
                // The set of machines changed: another job follows that one.
                if (shown.map { it.deviceId } != ids) return@launch
                // One at a time, round and round; one the person asked about by name goes first.
                val named = nudgeTarget?.let { id -> shown.firstOrNull { it.deviceId == id } }
                nudgeTarget = null
                machine = named ?: shown[turn % shown.size].also { turn++ }
                val t = clock()
                val limitedUntil = rateLimitedUntil[machine.discovery] ?: 0L
                val allowed = appForeground && t >= limitedUntil &&
                    (scheduled || t - lastStart >= readinessMinGapMillis)
                if (allowed) {
                    lastStart = t
                    val answer = withContext(io) {
                        try {
                            checkStatus(machine)
                        } catch (e: Exception) {
                            StatusAnswer.Known(Readiness(ReadinessState.NO_NETWORK))
                        }
                    }
                    readinessNudges.tryReceive() // asked for while this one ran: this is that answer
                    val now = _state.value
                    if (followedMachines(now).none { it.deviceId == machine.deviceId }) return@launch
                    when (answer) {
                        is StatusAnswer.Known -> {
                            rememberReadiness(machine, answer.readiness)
                            appLog.d { "status ${machine.deviceId} ${answer.readiness.state.name.lowercase()} age=${answer.readiness.ageSeconds}" }
                            when (now) {
                                is UnlockUiState.Ready ->
                                    if (now.readiness != answer.readiness) _state.value = now.copy(readiness = answer.readiness)
                                is UnlockUiState.Machines ->
                                    if (now.readiness[machine.deviceId] != answer.readiness) {
                                        _state.value = now.copy(readiness = now.readiness + (machine.deviceId to answer.readiness))
                                    }
                                else -> Unit
                            }
                        }
                        is StatusAnswer.RateLimited -> {
                            rateLimitedUntil[machine.discovery] = clock() + answer.retryAfterMillis
                            appLog.d { "status rate limited, not asking for ${answer.retryAfterMillis / 1000} s" }
                        }
                    }
                }
                val untilAllowed = (rateLimitedUntil[machine.discovery] ?: 0L) - clock()
                // Each machine about as often as one alone would be, and never more than a few
                // looks a minute in all, however many machines there are.
                val gap = readinessPollMillis / minOf(shown.size, MAX_READINESS_LOOKUPS_PER_POLL)
                val wait = maxOf(readinessPollDelayMillis(gap, readinessJitter()), untilAllowed)
                val nudged = withTimeoutOrNull(wait) { readinessNudges.receive(); true } ?: false
                scheduled = !nudged
                if (followedMachines(_state.value).isEmpty()) return@launch
            }
        }
    }

    private fun rememberReadiness(machine: Machine, readiness: Readiness) {
        lastReadiness[machine.deviceId] = readiness
        lastReadinessAt[machine.deviceId] = clock()
    }

    /**
     * What discovery knows of [machine] now that a dial has given up on it: one lookup, unless
     * the server has asked to be left alone, in which case (and when the lookup is told so)
     * what the chip last knew, as old as it has grown since. Null when nothing is known.
     */
    private suspend fun statusAtFailure(machine: Machine): Readiness? {
        if (clock() >= (rateLimitedUntil[machine.discovery] ?: 0L)) {
            val answer = withContext(io) {
                try {
                    checkStatus(machine)
                } catch (e: Exception) {
                    StatusAnswer.Known(Readiness(ReadinessState.NO_NETWORK))
                }
            }
            when (answer) {
                is StatusAnswer.Known -> {
                    rememberReadiness(machine, answer.readiness)
                    appLog.d { "status at failure ${answer.readiness.state.name.lowercase()} age=${answer.readiness.ageSeconds}" }
                    return answer.readiness
                }
                is StatusAnswer.RateLimited -> {
                    rateLimitedUntil[machine.discovery] = clock() + answer.retryAfterMillis
                    appLog.d { "status at failure rate limited, using the last known" }
                }
            }
        }
        val last = lastReadiness[machine.deviceId] ?: return null
        val seenAt = lastReadinessAt[machine.deviceId] ?: return last
        return agedSince(last, ((clock() - seenAt) / 1000).toInt())
    }

    private fun isUnreachable(e: Exception) = e is MachineFailure && e.kind == FailureKind.UNREACHABLE

    /**
     * A tap on Check again: look the machine up now, if the last look was not just now and
     * the discovery server has not asked to be left alone.
     */
    fun checkAgain(deviceId: String? = null) {
        appLog.d { "action check again" + if (deviceId != null) " id=$deviceId" else "" }
        nudgeTarget = deviceId
        readinessNudges.trySend(Unit)
    }

    val state: StateFlow<UnlockUiState> = _state.asStateFlow()

    // A failure, to the log hook and to the log at warn, with its whole cause chain.
    private fun logWarn(message: String, e: Throwable) {
        log(message, e)
        appLog.w({ message }, e)
    }

    // One key operation, timed, for the log: when it started and ended and how long it took,
    // and nothing it handled.
    private inline fun <T> timed(what: String, block: () -> T): T {
        appLog.d { "$what start" }
        val started = clock()
        try {
            return block()
        } finally {
            val took = clock() - started
            appLog.d { "$what end ${took} ms" }
        }
    }

    private var session: MachineSession? = null

    // Where the busy screens read progress from, and the coroutine that does; see [startPolling].
    @Volatile private var progressSource: (() -> Progress)? = null
    private var poller: Job? = null

    // Gives up on an Approve screen nobody answers; see [armApproveExpiry].
    private var approveExpiry: Job? = null

    // The dial or pairing now running, if any, so that cancel() can
    // interrupt it rather than decline over a connection that does
    // not exist yet.
    private var inFlight: InFlight? = null

    /**
     * Generates the long-term key if this is the first run, then shows
     * either the pairing screen or the unlock screen.
     */
    fun refresh() {
        appLog.d { "action refresh" }
        scope.launch {
            _state.value = home()
            checkKeyOnce()
        }
    }

    private var keyChecked = false
    private var keyCheckRunning = false

    /**
     * The app came back to the foreground, which is when someone may have enrolled a
     * finger in Settings, retiring the key: check again, behind whatever is showing.
     */
    fun returnedToForeground() {
        appLog.d { "action returned to foreground" }
        appForeground = true
        readinessNudges.trySend(Unit)
        runKeyCheck()
    }

    private fun checkKeyOnce() {
        if (!keyChecked) runKeyCheck()
    }

    private fun runKeyCheck() {
        val shown = _state.value
        if (keyCheckRunning || (shown !is UnlockUiState.Ready && shown !is UnlockUiState.Machines && shown !is UnlockUiState.NeedsPairing)) return
        keyChecked = true
        keyCheckRunning = true
        scope.launch {
            try {
                withContext(io) { runWithin(keyUseTimeoutSeconds) { checkKey() } }
            } catch (e: KeyInvalidated) {
                val now = _state.value
                // Not while a dial is under way or a request is waiting (the screen is
                // then Connecting or Approve): that session has this identity, and the
                // approval finds the dead key for itself.
                if (now is UnlockUiState.Ready || now is UnlockUiState.Machines || now is UnlockUiState.NeedsPairing) {
                    logWarn("the key was found invalidated at start", e)
                    val oldDeviceId = withContext(io) { resetIdentity() }
                    _state.value = UnlockUiState.Failed(
                        null,
                        strings.get(R.string.failure_key_invalidated, oldDeviceId),
                        Retry.SCAN_AGAIN,
                    )
                }
            } catch (e: Exception) {
                // Not knowing is not a reason to show anything: the next Approve
                // starts the same operation and reports what it finds.
                logWarn("could not check the key at start", e)
            } finally {
                keyCheckRunning = false
            }
        }
    }

    /**
     * Shows the pairing screen while a machine is already paired. That
     * machine stays paired, and stays the one Unlock dials, until a
     * new pairing has actually succeeded; [keepCurrentMachine] goes
     * back to it.
     */
    fun startPairing() {
        appLog.d { "action start pairing" }
        scope.launch { _state.value = home(forcePairing = true) }
    }

    fun keepCurrentMachine() {
        appLog.d { "action keep current machine" }
        refresh()
    }

    /**
     * The person has chosen to throw away a key that was not whole and make a
     * new one, with a new transport identity ([resetIdentity]); the screen then
     * says which Device ID to revoke on the machines that trusted the old one.
     */
    fun replaceKey() {
        appLog.d { "action replace key" }
        scope.launch {
            val oldDeviceId = withContext(io) { resetIdentity() }
            _state.value = UnlockUiState.Failed(
                null,
                strings.get(R.string.failure_key_replaced, oldDeviceId),
                Retry.REFRESH,
            )
        }
    }

    /**
     * The owner has accepted a software-backed key on this phone. Recorded
     * by [allowSoftwareKey]; the flow then carries on as if the key had
     * been fine.
     */
    fun allowSoftwareKey() {
        appLog.d { "action allow software key" }
        allowSoftwareKey.invoke()
        refresh()
    }

    /**
     * Removes a machine from this phone: the entry, and nothing else. A machine that enrolled this phone keeps
     * it enrolled until its key holder is revoked there. Only from the home screens, not while something is
     * under way. The screen offers to take it back ([undoForget]).
     */
    fun forget(deviceId: String) {
        appLog.d { "action forget id=$deviceId" }
        val shown = _state.value
        if (shown !is UnlockUiState.Ready && shown !is UnlockUiState.Machines) return
        val machine = store.machine(deviceId) ?: return
        store.forget(deviceId)
        machinesChanged()
        scope.launch { _state.value = home(forgotten = machine) }
    }

    private fun forgottenOf(state: UnlockUiState): Machine? = when (state) {
        is UnlockUiState.Ready -> state.forgotten
        is UnlockUiState.Machines -> state.forgotten
        is UnlockUiState.NeedsPairing -> state.forgotten
        else -> null
    }

    /** Takes back the machine that has just been forgotten. */
    fun undoForget() {
        appLog.d { "action undo forget" }
        val machine = forgottenOf(_state.value) ?: return
        store.saveMachine(machine)
        machinesChanged()
        refresh()
    }

    /** The offer to take a machine back has gone unused; stop offering it. */
    fun dismissUndo() {
        appLog.d { "action dismiss undo" }
        _state.value = when (val shown = _state.value) {
            is UnlockUiState.Ready -> shown.copy(forgotten = null)
            is UnlockUiState.Machines -> shown.copy(forgotten = null)
            is UnlockUiState.NeedsPairing -> shown.copy(forgotten = null)
            else -> return
        }
    }

    /** Saves [machine] (adds it, or updates the one with its Device ID) and shows the home screen. */
    private suspend fun adopt(machine: Machine) {
        store.saveMachine(machine)
        machinesChanged()
        _state.value = home()
    }

    private suspend fun home(forcePairing: Boolean = false, forgotten: Machine? = null): UnlockUiState =
        try {
            val identity = withContext(io) { identity() }
            val machines = store.machines()
            when {
                machines.isEmpty() || forcePairing -> UnlockUiState.NeedsPairing(identity, machines, forgotten)
                machines.size == 1 -> UnlockUiState.Ready(
                    machines[0], identity, forgotten, lastReadiness[machines[0].deviceId] ?: Readiness(),
                )
                else -> UnlockUiState.Machines(
                    machines, identity,
                    machines.mapNotNull { m -> lastReadiness[m.deviceId]?.let { m.deviceId to it } }.toMap(),
                    forgotten,
                )
            }
        } catch (e: SoftwareKeyRefused) {
            UnlockUiState.SoftwareKeyRefused
        } catch (e: KeyNeedsReplacing) {
            logWarn("the key is not whole", e)
            UnlockUiState.KeyNeedsReplacing(e.problem)
        } catch (e: BiometricEnrolmentRequired) {
            UnlockUiState.Failed(null, strings.get(R.string.error_no_biometric))
        } catch (e: Exception) {
            // The screen only ever shows a one-line message, so
            // without this the stack behind an unexpected failure is
            // gone. Finding out that the Keystore wraps its "no
            // biometric enrolled" error took a throwaway build added
            // just to print this.
            logWarn("could not prepare this phone's identity", e)
            failed(null, e)
        }

    /**
     * Takes a scanned (or typed) pairing payload, and reports whether
     * it was one.
     *
     * A payload carrying a one-time code finishes enrolment by itself:
     * the phone dials the machine, proves it holds the code and sends
     * its public key. The machine is saved only once that has worked.
     * Saving it first bought nothing, since the code is single-use and
     * a retry needs a fresh scan anyway, and it cost a lot: a failed
     * pairing left a machine that had never enrolled this phone sitting
     * on the Unlock screen, and a failed attempt to pair a second
     * machine overwrote the first, working one.
     *
     * Without a code all that has been scanned is a machine to
     * remember, which is what enrolment looked like before, so it is
     * saved straight away and the enrol command is what remains. It adds
     * the machine; a machine saved by mistake is forgotten from the list.
     */
    fun pair(scanned: String): Boolean {
        // The machine, and only whether there was a code: never the code, never the link.
        appLog.d {
            "action pair machine=${PairingPayload.parse(scanned)?.deviceId ?: "none"} " +
                "code=${if (PairingPayload.pairingCode(scanned) != null) "yes" else "no"}"
        }
        when (val plan = planPairing(scanned)) {
            PairingPlan.Invalid -> return false
            is PairingPlan.Remember -> scope.launch { adopt(plan.machine) }
            is PairingPlan.Enrol -> enrol(plan.machine, plan.code)
            // Shown as a failure rather than as "not a pairing link": it is one, and
            // the person needs to hear that this version of the app cannot honour it.
            is PairingPlan.UnknownParameters -> _state.value = UnlockUiState.Failed(
                null,
                strings.get(R.string.pair_unknown_parameters, plan.names.take(MAX_NAMES_SHOWN).joinToString(", ")),
                Retry.SCAN_AGAIN,
            )
            is PairingPlan.RepeatedParameters -> _state.value = UnlockUiState.Failed(
                null,
                strings.get(R.string.pair_repeated_parameters, plan.names.take(MAX_NAMES_SHOWN).joinToString(", ")),
                Retry.SCAN_AGAIN,
            )
            is PairingPlan.BadSetting -> _state.value = UnlockUiState.Failed(
                null,
                strings.get(R.string.pair_bad_setting, plan.name),
                Retry.SCAN_AGAIN,
            )
            PairingPlan.PlainHttpDiscovery -> _state.value = UnlockUiState.Failed(
                null,
                strings.get(R.string.pair_plain_http_discovery),
                Retry.SCAN_AGAIN,
            )
        }
        return true
    }

    private fun enrol(machine: Machine, code: String) {
        val attempt = InFlight()
        inFlight = attempt
        _state.value = UnlockUiState.Pairing(machine, Progress(startedAtMillis = clock()))
        startPolling()

        scope.launch {
            try {
                val identity = withContext(io) { identity() }
                withContext(io) {
                    val pairing = newPairing()
                    progressSource = { pairing.progress() }
                    attempt.bind { pairing.cancel() }
                    pairing.pair(machine, code, identity)
                }
                // Saved even if Cancel came too late to stop it: the
                // machine has enrolled this phone, and saying otherwise
                // would leave a working pairing unused.
                appLog.d { "pairing succeeded machine=${machine.name} id=${machine.deviceId}" }
                adopt(machine)
            } catch (e: Exception) {
                if (attempt.cancelled) {
                    _state.value = home(forcePairing = true)
                } else if (e is SoftwareKeyRefused) {
                    _state.value = UnlockUiState.SoftwareKeyRefused
                } else if (e is KeyNeedsReplacing) {
                    logWarn("the key is not whole", e)
                    _state.value = UnlockUiState.KeyNeedsReplacing(e.problem)
                } else {
                    _state.value = failed(machine, e, Retry.SCAN_AGAIN)
                }
            } finally {
                if (inFlight === attempt) inFlight = null
            }
        }
    }

    /**
     * Reads how far the Go side has got, every [pollIntervalMillis], for as long as the
     * screen is a busy one, and puts it into the state. The loop ends by itself the first
     * time it finds the state is not busy, without asking again, so nothing keeps polling
     * once a dial, a pairing or an answer is over. Runs on [scope], so it never races the
     * assignments to the state.
     */
    private fun startPolling() {
        poller?.cancel()
        poller = scope.launch {
            while (true) {
                delay(pollIntervalMillis)
                val shown = _state.value
                val current = when (shown) {
                    is UnlockUiState.Connecting -> shown.progress
                    is UnlockUiState.Pairing -> shown.progress
                    is UnlockUiState.Working -> shown.progress
                    else -> return@launch
                }
                val read = progressSource?.invoke() ?: continue
                val next = read.copy(startedAtMillis = current.startedAtMillis)
                if (next == current) continue
                _state.value = when (shown) {
                    is UnlockUiState.Connecting -> shown.copy(progress = next)
                    is UnlockUiState.Pairing -> shown.copy(progress = next)
                    is UnlockUiState.Working -> shown.copy(progress = next)
                    else -> return@launch
                }
            }
        }
    }

    /**
     * Dials the machine and waits for it to say what it wants. With a [gate], the
     * fingerprint prompt then comes up by itself as soon as the request arrives, so one
     * tap does the whole unlock; without one, the flow stops on the Approve screen.
     */
    fun connect(gate: BiometricGate? = null) {
        connectTo(store.machine() ?: return, gate)
    }

    /** The same for one machine on the list. */
    fun connect(machine: Machine, gate: BiometricGate? = null) {
        connectTo(machine, gate)
    }

    // An unlock asked for from outside the app, which came before the home screen was there.
    private var waitingUnlock: Pair<String, BiometricGate>? = null
    private var waitingUnlockAt = 0L
    private var homeShown = false

    /**
     * An unlock asked for from outside the app (a shortcut, a widget): the one-tap unlock of the machine
     * with this Device ID, which is a machine this phone is paired with and no other. It dials, and the
     * fingerprint prompt is what lets the answer go, exactly as for the Unlock button.
     *
     * It does not interrupt anything: while a dial, a pairing or an approval is under way, or the key needs
     * a decision, it is dropped (the app is in front showing what it was). Before the home screen is
     * ready it waits for it, for [PENDING_UNLOCK_MILLIS] at most. From the home screen, a finished unlock, a
     * failure and the pairing screen it starts at once.
     */
    fun unlockRequested(deviceId: String, gate: BiometricGate) {
        appLog.d { "action unlock requested from outside id=$deviceId" }
        if (!homeShown) {
            waitingUnlock = deviceId to gate
            waitingUnlockAt = clock()
            return
        }
        when (_state.value) {
            is UnlockUiState.Ready, is UnlockUiState.Machines, is UnlockUiState.Done, is UnlockUiState.Failed,
            is UnlockUiState.NeedsPairing -> startRequestedUnlock(deviceId, gate)
            else -> appLog.d { "unlock requested from outside dropped: busy" }
        }
    }

    private fun startRequestedUnlock(deviceId: String, gate: BiometricGate) {
        val machine = store.machine(deviceId)
        if (machine == null) {
            appLog.d { "unlock requested from outside dropped: not a paired machine" }
            return
        }
        connectTo(machine, gate)
    }

    /** The first state after the app started: what an unlock that came too early was waiting for. */
    private fun startWaitingUnlock(next: UnlockUiState) {
        if (homeShown) return
        homeShown = true
        val (deviceId, gate) = waitingUnlock ?: return
        waitingUnlock = null
        if ((next !is UnlockUiState.Ready && next !is UnlockUiState.Machines) || clock() - waitingUnlockAt > PENDING_UNLOCK_MILLIS) {
            appLog.d { "unlock requested from outside dropped: not ready in time" }
            return
        }
        scope.launch { startRequestedUnlock(deviceId, gate) }
    }

    private fun connectTo(machine: Machine, gate: BiometricGate?) {
        appLog.d { "action connect machine=${machine.name} id=${machine.deviceId}" + if (gate != null) " one-tap" else "" }
        // The machine unlocked last comes first on the list, in the shortcuts and on the widget.
        store.markUsed(machine.deviceId)
        machinesChanged()
        disarmApproveExpiry()
        val attempt = InFlight()
        inFlight = attempt
        _state.value = UnlockUiState.Connecting(machine, Progress(startedAtMillis = clock()))
        startPolling()

        scope.launch {
            var fresh: MachineSession? = null
            var connectedAt = clock()
            try {
                withContext(io) {
                    closeSession()
                    val dialled = newSession(machine)
                    progressSource = { dialled.progress() }
                    fresh = dialled
                    attempt.bind { dialled.cancel() }
                    dialled.connect()
                    // The machine's session has been running since the connection was
                    // made, which is before the request arrived if it was slow to send it,
                    // and it is the machine's side, or the relay between, that runs out of
                    // patience. The dial before that does not count: no session existed.
                    connectedAt = clock() - dialled.connectedForMillis()
                }
                session = fresh
                if (attempt.cancelled) {
                    // Cancelled just as the dial succeeded: the machine
                    // is now waiting, so tell it, as Decline would.
                    declineAndGoHome(DECLINED_BY_KEY_HOLDER)
                } else {
                    val expiresAt = connectedAt + approveWindowMillis
                    approveExpiresAtMillis = expiresAt
                    _state.value = describe(machine).let { if (it is UnlockUiState.Approve) it.copy(expiresAtMillis = expiresAt) else it }
                    (_state.value as? UnlockUiState.Approve)?.let { armApproveExpiry(machine, expiresAt) }
                    // Asked to unlock in one tap and the machine did ask: the person has
                    // already chosen this machine, and the fingerprint is the consent.
                    if (gate != null && _state.value is UnlockUiState.Approve) approve(gate)
                }
            } catch (e: Exception) {
                withContext(io) { fresh?.closeQuietly() }
                // The dial gave up: say what discovery knows of the machine, in the headline.
                val known = if (!attempt.cancelled && isUnreachable(e)) statusAtFailure(machine) else null
                _state.value = if (attempt.cancelled) {
                    home()
                } else {
                    failed(machine, e, known = known)
                }
            } finally {
                if (inFlight === attempt) inFlight = null
            }
        }
    }

    /**
     * Approves the request: prepare one use of the key, have the person's
     * fingerprint authenticate exactly that use, unwrap the key with it and
     * send the answer.
     *
     * The gate is the hardware's: the cipher can unwrap nothing until the
     * prompt has authenticated it, so skipping the prompt would simply make the
     * unwrapping throw.
     */
    fun approve(gate: BiometricGate) {
        // One approval per request: a second press, or a key, queued
        // behind the first would start a second prompt over the same
        // session.
        val waiting = _state.value as? UnlockUiState.Approve ?: return
        // The machine as this phone knows it, by name; the Approve state carries what the machine called itself.
        val machine = store.machine(waiting.machine.deviceId) ?: waiting.machine
        val active = session ?: return
        appLog.d { "action approve machine=${machine.name}" }
        // The window to answer is not ended by tapping Approve: the fingerprint prompt
        // that follows is part of it, so the expiry stays armed until the answer is
        // actually being sent (see [ApproveRun]).
        val window = ApproveRun()
        approveRun = window
        _state.value = UnlockUiState.Working(machine, Progress(startedAtMillis = clock()))
        progressSource = { active.progress() }
        startPolling()

        window.job = scope.launch {
            try {
                approveAndAnswer(window, machine, active, gate)
            } finally {
                window.finished = true
            }
        }
    }

    /**
     * One approval, from preparing the key to the machine's verdict. [window] says when the
     * window to answer has run out, which can happen while the fingerprint prompt is up.
     */
    private suspend fun approveAndAnswer(window: ApproveRun, machine: Machine, active: MachineSession, gate: BiometricGate) {
        // Before any prompt: a key that was retired, or is missing, is found
        // here, and nobody is asked to touch the sensor for an answer that
        // cannot be given.
        val cipher = try {
            withContext(io) { timed("prepare cipher") { runWithin(keyUseTimeoutSeconds) { prepareKey() } } }
        } catch (e: KeyInvalidated) {
            disarmApproveExpiry()
            retireInvalidatedKey(machine, active, e)
            return
        } catch (e: Exception) {
            disarmApproveExpiry()
            declineQuietly(active)
            withContext(io) { closeSession() }
            _state.value = failed(machine, e)
            return
        }

        appLog.d { "biometric prompt shown" }
        val outcome = try {
            gate.authenticate(
                title = strings.get(R.string.biometric_title),
                subtitle = strings.get(R.string.biometric_subtitle, machine.name.ifBlank { machine.deviceId }),
                cipher = cipher,
            )
        } catch (e: kotlinx.coroutines.CancellationException) {
            appLog.d { "biometric prompt cancelled" + if (window.expired) " by the window" else "" }
            throw e
        }
        appLog.d { "biometric prompt result $outcome" }
        // Whichever comes first wins. If the window ran out while the prompt was up, the
        // machine has been told no and the screen says so, and a touch that lands now, from a
        // prompt that did not notice being cancelled, answers nothing. Everything here runs
        // on the one thread, so the flag and the answer cannot interleave.
        if (window.expired) return
        currentCoroutineContext().ensureActive()
        if (outcome == AuthOutcome.CANCELLED) {
            // The person changed their mind about the prompt, or it was dismissed. The
            // machine is still waiting and the window is still running (and stays armed),
            // so the request is where it was: back to the Approve screen, to try again or
            // to decline, and the machine is told nothing.
            _state.value = describe(machine).let { if (it is UnlockUiState.Approve) it.copy(expiresAtMillis = approveExpiresAtMillis) else it }
            return
        }
        if (outcome != AuthOutcome.SUCCEEDED) {
            disarmApproveExpiry()
            // Tell the machine rather than leaving it waiting for a
            // timeout: it can then say why it is still locked.
            withContext(io) {
                try {
                    active.decline(KEY_HOLDER_NOT_AUTHENTICATED)
                } catch (e: Exception) {
                    // The machine may already have hung up; the
                    // user-visible outcome is the same either way.
                }
                closeSession()
            }
            _state.value = UnlockUiState.Failed(machine, strings.get(failureMessage(outcome)))
            return
        }

        // From here the answer is being sent, under its own time limits; the window is over.
        window.answering = true
        disarmApproveExpiry()
        try {
            withContext(io) { active.answer { timed("unwrap key") { runWithin(keyUseTimeoutSeconds, discard = { it.fill(0) }) { unwrapKey(cipher) } } } }
            _state.value = UnlockUiState.Done(machine)
        } catch (e: KeyUseTimedOut) {
            // The key is not known to be broken, so it is kept; the
            // machine is told, rather than left to its own timeout.
            declineQuietly(active)
            _state.value = failed(machine, e)
        } catch (e: KeyRefusedFingerprint) {
            // Kept, and the machine told, for the same reason; see the class.
            declineQuietly(active)
            _state.value = failed(machine, e)
        } catch (e: MachineFailure) {
            if (e.kind == FailureKind.KEY_MISMATCH) {
                // Nothing was sent: this phone's own two files disagree. Not the
                // machine's doing, and not the app's to repair by deleting.
                logWarn("the saved key does not match its public point", e)
                declineQuietly(active)
                _state.value = UnlockUiState.KeyNeedsReplacing(KeyProblem.FILES_DO_NOT_MATCH)
            } else {
                _state.value = failed(machine, e)
            }
        } catch (e: Exception) {
            _state.value = failed(machine, e)
        } finally {
            withContext(io) { closeSession() }
        }
    }

    /**
     * Nothing was sent, and nothing this key does will ever work again: tell
     * the machine no, and start afresh with a new key and a new transport
     * identity. A new identity, because the machine answers the first recipient
     * whose identity matches, so pairing again under the old one would leave the
     * dead key in front.
     */
    private suspend fun retireInvalidatedKey(machine: Machine, active: MachineSession, cause: KeyInvalidated) {
        logWarn("the key was invalidated", cause)
        declineQuietly(active)
        val oldDeviceId = withContext(io) { resetIdentity() }
        withContext(io) { closeSession() }
        _state.value = UnlockUiState.Failed(
            machine,
            strings.get(R.string.failure_key_invalidated, oldDeviceId),
            Retry.SCAN_AGAIN,
        )
    }

    /**
     * Backs out of whatever is happening, without answering anything.
     * A dial or pairing still running is interrupted; a machine that is
     * already waiting for this phone is told no, rather than left to
     * wait for its timeout. Decline, Cancel, Escape and back all come
     * here.
     */
    fun cancel() {
        appLog.d { "action cancel in " + describeForLog(_state.value).substringBefore(' ') }
        disarmApproveExpiry()
        inFlight?.let {
            it.cancel()
            return
        }
        scope.launch { declineAndGoHome(DECLINED_BY_KEY_HOLDER) }
    }

    private suspend fun declineAndGoHome(reason: String) {
        withContext(io) {
            try {
                session?.decline(reason)
            } catch (e: Exception) {
                // Already gone; nothing useful to report.
            }
            closeSession()
        }
        _state.value = home()
    }

    private fun failed(machine: Machine?, e: Exception, retry: Retry = Retry.REFRESH, known: Readiness? = null): UnlockUiState.Failed {
        logWarn("failed", e)
        val explained = explain(e)
        val headline = known?.takeIf { isUnreachable(e) }?.let { unreachableHeadline(it) }
        val message = if (headline == null) {
            strings.get(explained.message)
        } else {
            val args = headline.age?.let { arrayOf<Any>(strings.get(it.res, *it.args.toTypedArray())) } ?: emptyArray()
            strings.get(headline.res, *args)
        }
        return UnlockUiState.Failed(
            machine,
            message,
            retry,
            explained.detail,
            explained.technical + lastKnownStatus(machine, e),
        )
    }

    /**
     * After a dial that could not reach the machine, what discovery last said about it,
     * for the technical details (never the headline): "It last announced itself 47 min ago"
     * says more than a timeout does. Nothing if it was never looked up, or was still being.
     */
    private fun lastKnownStatus(machine: Machine?, e: Exception): String {
        if (machine == null || e !is MachineFailure || e.kind != FailureKind.UNREACHABLE) return ""
        val last = lastReadiness[machine.deviceId] ?: return ""
        if (last.state == ReadinessState.CHECKING) return ""
        val label = readinessLabel(last)
        return "\n" + strings.get(R.string.failure_last_status, strings.get(label.res, *label.args.toTypedArray()))
    }

    private suspend fun declineQuietly(active: MachineSession) = withContext(io) {
        try {
            active.decline(DECLINED_BY_KEY_HOLDER)
        } catch (e: Exception) {
            // The machine may already have hung up.
        }
    }

    private fun describe(machine: Machine): UnlockUiState {
        val active = session ?: return UnlockUiState.Failed(machine, strings.get(R.string.error_connection_lost))
        val named = machine.copy(name = active.machineName().ifBlank { machine.name })
        return when {
            active.hasRequest() -> UnlockUiState.Approve(named, active.peerId(), active.requestKidHex())
            else -> UnlockUiState.Failed(named, strings.get(R.string.error_nothing_to_approve))
        }
    }

    /**
     * An Approve screen that nobody answers gives up: it tells the machine no,
     * closes the session and says what happened. Left alone it offered an
     * Approve that could only fail, for as long as the app stayed open.
     */
    private fun armApproveExpiry(machine: Machine, expiresAtMillis: Long) {
        appLog.d { "window armed ${(expiresAtMillis - clock()).coerceAtLeast(0)} ms" }
        approveExpiry?.cancel()
        approveExpiry = scope.launch {
            delay((expiresAtMillis - clock()).coerceAtLeast(0))
            // Still waiting for an answer: on the Approve screen, or past it with the
            // fingerprint prompt up and nothing sent yet.
            val window = approveRun
            val waiting = when (_state.value) {
                is UnlockUiState.Approve -> true
                is UnlockUiState.Working -> window != null && !window.answering && !window.finished
                else -> false
            }
            if (!waiting) return@launch
            appLog.d { "window expired, " + (if (window != null) "prompt cancelled" else "no prompt up") }
            window?.expired = true
            // Cancelling the approval cancels the prompt: the gate's coroutine ends, which
            // is what dismisses BiometricPrompt.
            window?.job?.cancel()
            logWarn("the approval window ran out", IllegalStateException("no answer in ${approveWindowMillis} ms from the connection"))
            withContext(io) {
                try {
                    session?.decline(APPROVAL_TIMED_OUT)
                } catch (e: Exception) {
                    // The machine has most likely hung up already.
                }
                closeSession()
            }
            _state.value = UnlockUiState.Failed(machine, strings.get(R.string.error_approval_expired))
        }
    }

    /**
     * One approval under way, from the tap on Approve to the answer. [expired] is set by
     * the window running out, [answering] once the answer is being sent (after which the
     * window no longer applies), and [finished] when the approval has ended either way.
     * All three are only touched on [scope]'s thread.
     */
    private class ApproveRun {
        var job: Job? = null
        var expired = false
        var answering = false
        var finished = false
    }

    private var approveRun: ApproveRun? = null

    /** When the window to answer the request now on screen ends, on the flow's clock. */
    private var approveExpiresAtMillis = 0L

    private fun disarmApproveExpiry() {
        approveExpiry?.cancel()
        approveExpiry = null
    }

    private fun closeSession() {
        session?.closeQuietly()
        session = null
    }

    /**
     * Releases everything. Interrupts a dial or pairing still running,
     * which would otherwise carry on to its timeout with nobody left
     * to hear the result.
     */
    fun close() {
        disarmApproveExpiry()
        poller?.cancel()
        poller = null
        inFlight?.cancel()
        closeSession()
    }

    /**
     * A dial or pairing that [cancel] can interrupt. The Go object it
     * cancels is created on the IO thread, possibly after the user has
     * already pressed Cancel, so whichever of [bind] and [cancel] comes
     * second does the cancelling.
     */
    private class InFlight {
        @Volatile var cancelled = false
            private set

        @Volatile private var target: (() -> Unit)? = null

        fun bind(cancelIt: () -> Unit) {
            target = cancelIt
            if (cancelled) cancelIt()
        }

        fun cancel() {
            cancelled = true
            target?.invoke()
        }
    }
}

private fun MachineSession.closeQuietly() {
    try {
        close()
    } catch (e: Exception) {
        // Closing a connection that is already gone is not a failure
        // worth showing anyone.
    }
}

/**
 * The state in words for the log: its type and the fields that are not secret. Machine
 * names, device IDs, stages and times are; a key identifier is shortened to its first
 * eight characters and the identity's public key is left out.
 */
internal fun describeForLog(state: UnlockUiState): String {
    fun machine(m: Machine) = "machine=${m.name} id=${m.deviceId}"
    fun progress(p: Progress) = "stage=${p.stage.name.lowercase()} attempt=${p.attempt} route=${p.route.name.lowercase()}"
    return when (state) {
        is UnlockUiState.NeedsPairing -> "NeedsPairing phone=${state.identity.deviceId} kept=${state.kept.size}"
        is UnlockUiState.Ready -> "Ready ${machine(state.machine)} phone=${state.identity.deviceId}"
        is UnlockUiState.Machines -> "Machines count=${state.machines.size} phone=${state.identity.deviceId}"
        is UnlockUiState.Connecting -> "Connecting ${machine(state.machine)} ${progress(state.progress)}"
        is UnlockUiState.Pairing -> "Pairing ${machine(state.machine)} ${progress(state.progress)}"
        is UnlockUiState.Approve -> "Approve ${machine(state.machine)} peer=${state.peerId} kid=${kidPrefix(state.kidHex)} expiresAt=${state.expiresAtMillis}"
        is UnlockUiState.Working -> "Working ${machine(state.machine)} ${progress(state.progress)}"
        is UnlockUiState.Done -> "Done ${machine(state.machine)}"
        is UnlockUiState.SoftwareKeyRefused -> "SoftwareKeyRefused"
        is UnlockUiState.KeyNeedsReplacing -> "KeyNeedsReplacing problem=${state.problem}"
        is UnlockUiState.Failed -> "Failed message=${state.message} machine=${state.machine?.name ?: "none"}"
    }
}
