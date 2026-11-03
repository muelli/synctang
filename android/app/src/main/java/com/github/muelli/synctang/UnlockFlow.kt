// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes
import javax.crypto.Cipher
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

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
     * Scan a machine's QR code. [previous] is the machine already
     * paired, if any, which stays paired until a new pairing works.
     */
    data class NeedsPairing(val identity: Identity, val previous: Machine? = null) : UnlockUiState

    /**
     * Paired and idle; the Unlock button is the only thing to do.
     * [replaced] is the machine a pairing has just replaced, which the
     * screen offers to go back to.
     */
    data class Ready(val machine: Machine, val identity: Identity, val replaced: Machine? = null) : UnlockUiState

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

/** Which machine this phone is paired with. [PairingStore] is the real one. */
interface MachineStore {
    fun machine(): Machine?
    fun saveMachine(machine: Machine)
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
        if (state.previous != null) BackAction.KEEP_CURRENT_MACHINE else BackAction.EXIT
    is UnlockUiState.Ready, is UnlockUiState.Done, is UnlockUiState.Failed,
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

    /** The link's [name] setting (discovery) is not a server URL. */
    data class BadSetting(val name: String) : PairingPlan

    /** The link's discovery server is plain http without `?insecure`. */
    object PlainHttpDiscovery : PairingPlan
}

fun planPairing(scanned: String): PairingPlan {
    val machine = PairingPayload.parse(scanned) ?: return PairingPlan.Invalid
    PairingPayload.unknownParameters(scanned).takeIf { it.isNotEmpty() }
        ?.let { return PairingPlan.UnknownParameters(it) }
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
    // How often the busy screens ask the Go side how far it has got, and the clock their
    // elapsed time is counted on.
    private val pollIntervalMillis: Long = POLL_INTERVAL_MILLIS,
    private val clock: () -> Long = System::currentTimeMillis,
    private val strings: Strings,
    private val log: (String, Throwable) -> Unit = { _, _ -> },
) {
    private val _state = MutableStateFlow<UnlockUiState>(UnlockUiState.Working(Machine("", "")))
    val state: StateFlow<UnlockUiState> = _state.asStateFlow()

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
        scope.launch {
            _state.value = home(previous = null)
            checkKeyOnce()
        }
    }

    private var keyChecked = false
    private var keyCheckRunning = false

    /**
     * The app came back to the foreground, which is when someone may have enrolled a
     * finger in Settings, retiring the key: check again, behind whatever is showing.
     */
    fun returnedToForeground() = runKeyCheck()

    private fun checkKeyOnce() {
        if (!keyChecked) runKeyCheck()
    }

    private fun runKeyCheck() {
        val shown = _state.value
        if (keyCheckRunning || (shown !is UnlockUiState.Ready && shown !is UnlockUiState.NeedsPairing)) return
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
                if (now is UnlockUiState.Ready || now is UnlockUiState.NeedsPairing) {
                    log("the key was found invalidated at start", e)
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
                log("could not check the key at start", e)
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
        scope.launch { _state.value = home(previous = store.machine(), forcePairing = true) }
    }

    fun keepCurrentMachine() = refresh()

    /**
     * The person has chosen to throw away a key that was not whole and make a
     * new one, with a new transport identity ([resetIdentity]); the screen then
     * says which Device ID to revoke on the machines that trusted the old one.
     */
    fun replaceKey() {
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
        allowSoftwareKey.invoke()
        refresh()
    }

    /**
     * Goes back to the machine a pairing has just replaced. Only the
     * phone's side is undone: a machine that enrolled this phone keeps
     * it enrolled until it is revoked there.
     */
    fun undoReplace() {
        val ready = _state.value as? UnlockUiState.Ready ?: return
        val replaced = ready.replaced ?: return
        store.saveMachine(replaced)
        refresh()
    }

    /** The offer to undo has gone unused; stop offering it. */
    fun dismissUndo() {
        val ready = _state.value as? UnlockUiState.Ready ?: return
        _state.value = ready.copy(replaced = null)
    }

    /**
     * Saves [machine] as the paired one and shows it, offering to undo
     * if that replaced a different machine.
     */
    private suspend fun adopt(machine: Machine) {
        val replaced = store.machine()?.takeIf { it.deviceId != machine.deviceId }
        store.saveMachine(machine)
        val next = home(previous = null)
        _state.value = if (next is UnlockUiState.Ready) next.copy(replaced = replaced) else next
    }

    private suspend fun home(previous: Machine?, forcePairing: Boolean = false): UnlockUiState =
        try {
            val identity = withContext(io) { identity() }
            val machine = store.machine()
            if (machine == null || forcePairing) {
                UnlockUiState.NeedsPairing(identity, previous)
            } else {
                UnlockUiState.Ready(machine, identity)
            }
        } catch (e: SoftwareKeyRefused) {
            UnlockUiState.SoftwareKeyRefused
        } catch (e: KeyNeedsReplacing) {
            log("the key is not whole", e)
            UnlockUiState.KeyNeedsReplacing(e.problem)
        } catch (e: BiometricEnrolmentRequired) {
            UnlockUiState.Failed(null, strings.get(R.string.error_no_biometric))
        } catch (e: Exception) {
            // The screen only ever shows a one-line message, so
            // without this the stack behind an unexpected failure is
            // gone. Finding out that the Keystore wraps its "no
            // biometric enrolled" error took a throwaway build added
            // just to print this.
            log("could not prepare this phone's identity", e)
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
     * saved straight away and the enrol command is what remains. That
     * cannot fail, so nothing protects the machine it replaces except
     * the undo offered on the Unlock screen afterwards (see [adopt]).
     */
    fun pair(scanned: String): Boolean {
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
        val previous = store.machine()
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
                adopt(machine)
            } catch (e: Exception) {
                if (attempt.cancelled) {
                    _state.value = home(previous = previous, forcePairing = true)
                } else if (e is SoftwareKeyRefused) {
                    _state.value = UnlockUiState.SoftwareKeyRefused
                } else if (e is KeyNeedsReplacing) {
                    log("the key is not whole", e)
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

    /** Dials the machine and waits for it to say what it wants. */
    fun connect() {
        val machine = store.machine() ?: return
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
                    _state.value = describe(machine).let { if (it is UnlockUiState.Approve) it.copy(expiresAtMillis = expiresAt) else it }
                    (_state.value as? UnlockUiState.Approve)?.let { armApproveExpiry(machine, expiresAt) }
                }
            } catch (e: Exception) {
                withContext(io) { fresh?.closeQuietly() }
                _state.value = if (attempt.cancelled) {
                    home(previous = null)
                } else {
                    failed(machine, e)
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
        if (_state.value !is UnlockUiState.Approve) return
        val machine = store.machine() ?: return
        val active = session ?: return
        disarmApproveExpiry()
        _state.value = UnlockUiState.Working(machine, Progress(startedAtMillis = clock()))
        progressSource = { active.progress() }
        startPolling()

        scope.launch {
            // Before any prompt: a key that was retired, or is missing, is found
            // here, and nobody is asked to touch the sensor for an answer that
            // cannot be given.
            val cipher = try {
                withContext(io) { runWithin(keyUseTimeoutSeconds) { prepareKey() } }
            } catch (e: KeyInvalidated) {
                retireInvalidatedKey(machine, active, e)
                return@launch
            } catch (e: Exception) {
                declineQuietly(active)
                withContext(io) { closeSession() }
                _state.value = failed(machine, e)
                return@launch
            }

            val outcome = gate.authenticate(
                title = strings.get(R.string.biometric_title),
                subtitle = strings.get(R.string.biometric_subtitle, machine.name.ifBlank { machine.deviceId }),
                cipher = cipher,
            )
            if (outcome != AuthOutcome.SUCCEEDED) {
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
                return@launch
            }

            try {
                withContext(io) { active.answer { runWithin(keyUseTimeoutSeconds, discard = { it.fill(0) }) { unwrapKey(cipher) } } }
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
                    log("the saved key does not match its public point", e)
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
    }

    /**
     * Nothing was sent, and nothing this key does will ever work again: tell
     * the machine no, and start afresh with a new key and a new transport
     * identity. A new identity, because the machine answers the first recipient
     * whose identity matches, so pairing again under the old one would leave the
     * dead key in front.
     */
    private suspend fun retireInvalidatedKey(machine: Machine, active: MachineSession, cause: KeyInvalidated) {
        log("the key was invalidated", cause)
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
        _state.value = home(previous = null)
    }

    private fun failed(machine: Machine?, e: Exception, retry: Retry = Retry.REFRESH): UnlockUiState.Failed {
        log("failed", e)
        val explained = explain(e)
        return UnlockUiState.Failed(
            machine,
            strings.get(explained.message),
            retry,
            explained.detail,
            explained.technical,
        )
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
        approveExpiry?.cancel()
        approveExpiry = scope.launch {
            delay((expiresAtMillis - clock()).coerceAtLeast(0))
            if (_state.value !is UnlockUiState.Approve) return@launch
            log("the approval window ran out", IllegalStateException("no answer in ${approveWindowMillis} ms from the connection"))
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
