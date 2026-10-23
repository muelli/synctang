// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
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
 */
fun interface BiometricGate {
    suspend fun authenticate(title: String, subtitle: String): AuthOutcome
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

    data class Connecting(val machine: Machine) : UnlockUiState

    /** Enrolling this phone on [machine] over the network. */
    data class Pairing(val machine: Machine) : UnlockUiState

    /**
     * The machine is running with a confirm code. [code] is what it
     * sent over the wire; the user must check it against what the
     * machine is printing on its own console before relaying it back.
     */
    data class ConfirmCode(val machine: Machine, val peerId: String, val code: String) : UnlockUiState

    /** A recovery request has arrived and is waiting for approval. */
    data class Approve(val machine: Machine, val peerId: String, val kidHex: String) : UnlockUiState

    /** Authenticating, computing the answer, or sending it. */
    data class Working(val machine: Machine) : UnlockUiState

    data class Done(val machine: Machine) : UnlockUiState

    /**
     * [message] is in words for a person; [detail], when there is one,
     * is the machine's own reason or the raw error text behind it.
     */
    data class Failed(
        val machine: Machine?,
        val message: String,
        val retry: Retry = Retry.REFRESH,
        val detail: String? = null,
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

    /** Interrupts a [connect] running on another thread; returns at once. */
    fun cancel()

    fun close()
    fun decline(reason: String)
    fun machineName(): String
    fun peerId(): String
    fun confirmCodeChallenge(): String
    fun hasRequest(): Boolean
    fun requestKidHex(): String
    fun submitConfirmCode(code: String)

    /**
     * Answers the request with [agree] applied to the challenge
     * point's coordinates, and waits for the machine's verdict.
     */
    fun answer(agree: (ByteArray, ByteArray) -> ByteArray)
}

/** One network pairing. The real one wraps the gomobile `Pairer`. */
interface PairingAttempt {
    /** Blocks until the machine has enrolled this phone, or throws. */
    fun pair(machine: Machine, code: String, identity: Identity)

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

    /** Exactly what Refuse or Cancel on that screen does: [UnlockFlow.cancel]. */
    CANCEL,

    /** Leave the pairing screen for the machine already paired. */
    KEEP_CURRENT_MACHINE,

    /** Swallow it: an answer is on its way and cannot be taken back. */
    IGNORE,
}

/**
 * Back on a screen where the machine is waiting for this phone must
 * tell the machine, as Refuse does, rather than finish the activity
 * and leave it waiting for its timeout.
 *
 * Working is swallowed rather than cancelled: it covers the moments
 * after the answer has been computed, when closing the connection
 * would only turn "unlocked" into "may or may not have unlocked".
 */
fun backAction(state: UnlockUiState): BackAction = when (state) {
    is UnlockUiState.NeedsPairing ->
        if (state.previous != null) BackAction.KEEP_CURRENT_MACHINE else BackAction.EXIT
    is UnlockUiState.Ready, is UnlockUiState.Done, is UnlockUiState.Failed -> BackAction.EXIT
    is UnlockUiState.Connecting, is UnlockUiState.Pairing,
    is UnlockUiState.ConfirmCode, is UnlockUiState.Approve -> BackAction.CANCEL
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
}

fun planPairing(scanned: String): PairingPlan {
    val machine = PairingPayload.parse(scanned) ?: return PairingPlan.Invalid
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
    private val agree: (ByteArray, ByteArray) -> ByteArray,
    private val resetIdentity: () -> String,
    private val strings: Strings,
    private val log: (String, Throwable) -> Unit = { _, _ -> },
) {
    private val _state = MutableStateFlow<UnlockUiState>(UnlockUiState.Working(Machine("", "")))
    val state: StateFlow<UnlockUiState> = _state.asStateFlow()

    private var session: MachineSession? = null

    // The dial or pairing now running, if any, so that cancel() can
    // interrupt it rather than decline over a connection that does
    // not exist yet.
    private var inFlight: InFlight? = null

    /**
     * Generates the long-term key if this is the first run, then shows
     * either the pairing screen or the unlock screen.
     */
    fun refresh() {
        scope.launch { _state.value = home(previous = null) }
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
        }
        return true
    }

    private fun enrol(machine: Machine, code: String) {
        val previous = store.machine()
        val attempt = InFlight()
        inFlight = attempt
        _state.value = UnlockUiState.Pairing(machine)

        scope.launch {
            try {
                val identity = withContext(io) { identity() }
                withContext(io) {
                    val pairing = newPairing()
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
                } else {
                    log("pairing failed", e)
                    _state.value = failed(machine, e, Retry.SCAN_AGAIN)
                }
            } finally {
                if (inFlight === attempt) inFlight = null
            }
        }
    }

    /** Dials the machine and waits for it to say what it wants. */
    fun connect() {
        val machine = store.machine() ?: return
        val attempt = InFlight()
        inFlight = attempt
        _state.value = UnlockUiState.Connecting(machine)

        scope.launch {
            var fresh: MachineSession? = null
            try {
                withContext(io) {
                    closeSession()
                    val dialled = newSession(machine)
                    fresh = dialled
                    attempt.bind { dialled.cancel() }
                    dialled.connect()
                }
                session = fresh
                if (attempt.cancelled) {
                    // Cancelled just as the dial succeeded: the machine
                    // is now waiting, so tell it, as Refuse would.
                    declineAndGoHome(DECLINED_BY_KEY_HOLDER)
                } else {
                    _state.value = describe(machine)
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
     * Relays the confirm code back to the machine. The machine decides
     * whether it was right; a wrong one makes it hang up.
     */
    fun submitConfirmCode(code: String) {
        val machine = store.machine() ?: return
        val active = session ?: return
        _state.value = UnlockUiState.Working(machine)

        scope.launch {
            try {
                withContext(io) { active.submitConfirmCode(code) }
                _state.value = describe(machine)
            } catch (e: Exception) {
                withContext(io) { closeSession() }
                _state.value = failed(machine, e)
            }
        }
    }

    /**
     * Approves the request: prompt for a biometric, then do the ECDH in
     * the Keystore and send the answer.
     *
     * The prompt is not what enforces the gate. The key is created with
     * a short authentication validity window, so the Keystore itself
     * refuses the ECDH unless a strong biometric matched moments ago
     * (see KeyHolderKey). Skipping the prompt would simply make
     * [KeyHolderKey.agree] throw.
     */
    fun approve(gate: BiometricGate) {
        val machine = store.machine() ?: return
        val active = session ?: return
        _state.value = UnlockUiState.Working(machine)

        scope.launch {
            val outcome = gate.authenticate(
                title = strings.get(R.string.biometric_title),
                subtitle = strings.get(R.string.biometric_subtitle, machine.name.ifBlank { machine.deviceId }),
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
                withContext(io) { active.answer(agree) }
                _state.value = UnlockUiState.Done(machine)
            } catch (e: KeyInvalidated) {
                // Nothing was sent, and nothing this key does will ever
                // work again: tell the machine no, and start afresh
                // with a new key and a new transport identity. A new
                // identity, because the machine answers the first
                // recipient whose identity matches, so pairing again
                // under the old one would leave the dead key in front.
                log("the key was invalidated", e)
                declineQuietly(active)
                val oldDeviceId = withContext(io) { resetIdentity() }
                _state.value = UnlockUiState.Failed(
                    machine,
                    strings.get(R.string.failure_key_invalidated, oldDeviceId),
                    Retry.SCAN_AGAIN,
                )
            } catch (e: AuthenticationExpired) {
                declineQuietly(active)
                _state.value = failed(machine, e)
            } catch (e: Exception) {
                _state.value = failed(machine, e)
            } finally {
                withContext(io) { closeSession() }
            }
        }
    }

    /**
     * Backs out of whatever is happening, without answering anything.
     * A dial or pairing still running is interrupted; a machine that is
     * already waiting for this phone is told no, rather than left to
     * wait for its timeout. Refuse, Cancel, Escape and back all come
     * here.
     */
    fun cancel() {
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
        val explained = explain(e)
        return UnlockUiState.Failed(machine, strings.get(explained.message), retry, explained.detail)
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
        val code = active.confirmCodeChallenge()
        return when {
            code.isNotEmpty() -> UnlockUiState.ConfirmCode(named, active.peerId(), code)
            active.hasRequest() -> UnlockUiState.Approve(named, active.peerId(), active.requestKidHex())
            else -> UnlockUiState.Failed(named, strings.get(R.string.error_nothing_to_approve))
        }
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
