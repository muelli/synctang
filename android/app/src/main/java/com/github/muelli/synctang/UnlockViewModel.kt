// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile
import mobile.Session

/**
 * The outcome of a biometric prompt, as the unlock flow cares about it.
 */
enum class AuthOutcome { SUCCEEDED, CANCELLED, UNAVAILABLE }

/**
 * A biometric prompt, abstracted so the unlock flow does not depend on
 * an Activity. [BiometricGateImpl] is the real one; a test can supply
 * its own.
 */
fun interface BiometricGate {
    suspend fun authenticate(title: String, subtitle: String): AuthOutcome
}

/**
 * Everything the single Unlock screen can be showing.
 */
sealed interface UnlockUiState {
    /** No machine paired yet: scan its QR code. */
    data class NeedsPairing(val identity: Identity) : UnlockUiState

    /** Paired and idle; the Unlock button is the only thing to do. */
    data class Ready(val machine: Machine, val identity: Identity) : UnlockUiState

    data class Connecting(val machine: Machine) : UnlockUiState

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

    data class Failed(val machine: Machine?, val message: String) : UnlockUiState
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

/**
 * Drives one unlock from the phone's side: dial, show who is asking,
 * gate on a biometric, do the one ECDH in the Keystore, answer.
 *
 * Nothing here holds the volume secret, or anything from which it
 * could be derived. The machine's challenge point is blinded by an
 * ephemeral scalar this phone never sees, so the answer is useless to
 * anyone who intercepts it and tells this phone nothing about the
 * disk.
 */
class UnlockViewModel(application: Application) : AndroidViewModel(application) {

    private val store = PairingStore(application)
    // Alias and gating both come from TestMode, together: an ungated
    // key lives under its own alias so it can never be mistaken for
    // the real one.
    private val key = KeyHolderKey(
        alias = TestMode.aliasFor(TestMode.noBiometric),
        requireUserAuthentication = !TestMode.noBiometric,
    )

    // Null on a device that will not give one out, which costs local
    // discovery and nothing else; see MulticastLease.
    private val multicastLock = MulticastLease.lockFor(application)

    private val _state = MutableStateFlow<UnlockUiState>(UnlockUiState.Working(Machine("", "")))
    val state: StateFlow<UnlockUiState> = _state.asStateFlow()

    private var session: Session? = null

    init {
        refresh()
    }

    /**
     * Generates the long-term key if this is the first run, then shows
     * either the pairing screen or the unlock screen.
     */
    fun refresh() {
        viewModelScope.launch {
            _state.value = try {
                val identity = withContext(Dispatchers.IO) { identity() }
                val machine = store.machine()
                if (machine == null) {
                    UnlockUiState.NeedsPairing(identity)
                } else {
                    UnlockUiState.Ready(machine, identity)
                }
            } catch (e: BiometricEnrolmentRequired) {
                UnlockUiState.Failed(
                    null,
                    getApplication<Application>().getString(R.string.error_no_biometric),
                )
            } catch (e: Exception) {
                // The screen only ever shows a one-line message, so
                // without this the stack behind an unexpected failure
                // is gone. Finding out that the Keystore wraps its
                // "no biometric enrolled" error took a throwaway build
                // added just to print this.
                android.util.Log.e(LOG_TAG, "could not prepare this phone's identity", e)
                UnlockUiState.Failed(null, e.friendlyMessage())
            }
        }
    }

    private fun identity(): Identity {
        val backing = key.ensureKey()
        return Identity(
            deviceId = Mobile.deviceIDForSeed(store.identitySeed()),
            publicKeyHex = key.publicPoint().toHex(),
            backing = backing,
        )
    }

    /**
     * Takes a scanned (or typed) pairing payload.
     *
     * A payload carrying a one-time code finishes enrolment by itself:
     * the phone dials the machine, proves it holds the code and sends
     * its public key, so nobody has to copy a public key from a phone
     * screen onto a machine. Without a code all that has been scanned
     * is a machine to remember, which is what enrolment looked like
     * before, and the enrol command is shown instead.
     *
     * The machine is saved either way and before the dial, so a
     * pairing that fails halfway leaves something to retry against
     * rather than sending the user back to the camera.
     */
    fun pair(scanned: String): Boolean {
        val machine = PairingPayload.parse(scanned) ?: return false
        store.saveMachine(machine)

        val code = PairingPayload.pairingCode(scanned)
        if (code == null) {
            refresh()
            return true
        }

        _state.value = UnlockUiState.Working(machine)
        viewModelScope.launch {
            try {
                val identity = withContext(Dispatchers.IO) { identity() }
                withContext(Dispatchers.IO) {
                    // Local discovery needs the lock just as an unlock
                    // does: pairing dials over the same two transports.
                    MulticastLease.holding(multicastLock) {
                        Mobile.pair(
                            machine.deviceId,
                            code,
                            store.identitySeed(),
                            identity.publicKeyHex,
                            android.os.Build.MODEL ?: "phone",
                            "",
                            PAIR_TIMEOUT_SECONDS,
                        )
                    }
                }
                refresh()
            } catch (e: Exception) {
                android.util.Log.w(LOG_TAG, "pairing failed", e)
                _state.value = UnlockUiState.Failed(machine, e.friendlyMessage())
            }
        }
        return true
    }

    fun forgetMachine() {
        store.forgetMachine()
        refresh()
    }

    /** Dials the machine and waits for it to say what it wants. */
    fun connect() {
        val machine = store.machine() ?: return
        _state.value = UnlockUiState.Connecting(machine)

        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    closeSession()
                    val fresh = Session(machine.deviceId, store.identitySeed())
                    fresh.setTimeoutSeconds(CONNECT_TIMEOUT_SECONDS)
                    // The lock covers the whole dial, including its
                    // retries: the local-discovery leg races the relay
                    // for as long as connect() runs, and hears nothing
                    // without it. Released either way, so a machine
                    // that never answers does not leave it held.
                    MulticastLease.holding(multicastLock) { fresh.connect() }
                    session = fresh
                }
                _state.value = describe(machine)
            } catch (e: Exception) {
                closeSession()
                _state.value = UnlockUiState.Failed(machine, e.friendlyMessage())
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

        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { active.submitConfirmCode(code) }
                _state.value = describe(machine)
            } catch (e: Exception) {
                closeSession()
                _state.value = UnlockUiState.Failed(machine, e.friendlyMessage())
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

        viewModelScope.launch {
            val outcome = gate.authenticate(
                title = getApplication<Application>().getString(R.string.biometric_title),
                subtitle = getApplication<Application>().getString(
                    R.string.biometric_subtitle,
                    machine.name.ifBlank { machine.deviceId },
                ),
            )
            if (outcome != AuthOutcome.SUCCEEDED) {
                declineAfterFailedAuth(active, machine, outcome)
                return@launch
            }

            try {
                withContext(Dispatchers.IO) {
                    val peerX = active.requestPointAffineX()
                    val peerY = active.requestPointAffineY()
                    val xOnly = key.agree(peerX, peerY)
                    active.answerXOnly(xOnly)
                }
                _state.value = UnlockUiState.Done(machine)
            } catch (e: Exception) {
                _state.value = UnlockUiState.Failed(machine, e.friendlyMessage())
            } finally {
                closeSession()
            }
        }
    }

    private suspend fun declineAfterFailedAuth(
        active: Session,
        machine: Machine,
        outcome: AuthOutcome,
    ) {
        // Tell the machine rather than leaving it waiting for a
        // timeout: it can then say why it is still locked.
        try {
            withContext(Dispatchers.IO) { active.decline("key holder did not authenticate") }
        } catch (e: Exception) {
            // The machine may already have hung up; the user-visible
            // outcome is the same either way.
        }
        closeSession()
        val app = getApplication<Application>()
        _state.value = UnlockUiState.Failed(
            machine,
            when (outcome) {
                AuthOutcome.UNAVAILABLE -> app.getString(R.string.error_no_biometric)
                else -> app.getString(R.string.error_cancelled)
            },
        )
    }

    /**
     * Abandons an in-flight request without answering it, telling the
     * machine why rather than letting it wait for a timeout.
     */
    fun cancel() {
        viewModelScope.launch {
            withContext(Dispatchers.IO) {
                try {
                    session?.decline("declined by the key holder")
                } catch (e: Exception) {
                    // Already gone; nothing useful to report.
                }
            }
            closeSession()
            refresh()
        }
    }

    private fun describe(machine: Machine): UnlockUiState {
        val active = session ?: return UnlockUiState.Failed(machine, "connection lost")
        val named = machine.copy(name = active.machineName().ifBlank { machine.name })
        val code = active.confirmCodeChallenge()
        return when {
            code.isNotEmpty() -> UnlockUiState.ConfirmCode(named, active.peerID(), code)
            active.hasRequest() -> UnlockUiState.Approve(named, active.peerID(), active.requestKid().toHex())
            else -> UnlockUiState.Failed(named, "the machine sent nothing to approve")
        }
    }

    private fun closeSession() {
        try {
            session?.close()
        } catch (e: Exception) {
            // Closing a connection that is already gone is not a
            // failure worth showing anyone.
        }
        session = null
    }

    override fun onCleared() {
        closeSession()
        super.onCleared()
    }

    private companion object {
        const val CONNECT_TIMEOUT_SECONDS = 90L

        // Shorter than a connect: the machine is showing a code that
        // expires in minutes and somebody is standing in front of it,
        // so a pairing that has not happened by now has gone wrong and
        // saying so beats a longer wait.
        const val PAIR_TIMEOUT_SECONDS = 60L
    }
}

/**
 * gomobile turns every Go error into a plain Exception whose message is
 * the Go error string, which is written for a log and not for a person.
 * Keeping it is still better than swallowing it: it is the only thing
 * that distinguishes "the machine is not waiting" from "the relay is
 * unreachable".
 */
internal const val LOG_TAG = "synctang"

internal fun Exception.friendlyMessage(): String =
    message?.takeIf { it.isNotBlank() } ?: this::class.java.simpleName
