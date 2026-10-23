// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.StateFlow
import mobile.Mobile
import mobile.Pairer
import mobile.Session

/**
 * Drives one unlock from the phone's side: dial, show who is asking,
 * gate on a biometric, do the one ECDH in the Keystore, answer.
 *
 * Nothing here holds the volume secret, or anything from which it
 * could be derived. The machine's challenge point is blinded by an
 * ephemeral scalar this phone never sees, so the answer is useless to
 * anyone who intercepts it and tells this phone nothing about the
 * disk.
 *
 * The decisions live in [UnlockFlow], which has no Android in it so
 * that it can be tested on the JVM; this class supplies the Keystore,
 * the Go bridge, the store and the multicast lock behind it.
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

    private val flow = UnlockFlow(
        scope = viewModelScope,
        io = Dispatchers.IO,
        store = store,
        identity = ::identity,
        newSession = { machine -> GoSession(machine, store.identitySeed(), multicastLock) },
        newPairing = { GoPairing(store.identitySeed(), multicastLock) },
        agree = key::agree,
        resetIdentity = ::resetIdentity,
        strings = object : Strings {
            override fun get(id: Int, vararg args: Any) = application.getString(id, *args)
        },
        log = { message, e -> android.util.Log.w(LOG_TAG, message, e) },
    )

    val state: StateFlow<UnlockUiState> = flow.state

    init {
        refresh()
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
     * Retires this phone's invalidated key and transport identity,
     * returning the old Device ID so the person can revoke it on the
     * machine. The next [identity] creates both afresh.
     */
    private fun resetIdentity(): String {
        val old = Mobile.deviceIDForSeed(store.identitySeed())
        key.delete()
        store.startNewIdentity()
        return old
    }

    fun refresh() = flow.refresh()
    fun startPairing() = flow.startPairing()
    fun keepCurrentMachine() = flow.keepCurrentMachine()
    fun undoReplace() = flow.undoReplace()
    fun dismissUndo() = flow.dismissUndo()
    fun pair(scanned: String): Boolean = flow.pair(scanned)
    fun connect() = flow.connect()
    fun submitConfirmCode(code: String) = flow.submitConfirmCode(code)
    fun approve(gate: BiometricGate) = flow.approve(gate)
    fun cancel() = flow.cancel()

    override fun onCleared() {
        flow.close()
        super.onCleared()
    }
}

/** A [MachineSession] over the gomobile `Session`. */
private class GoSession(
    machine: Machine,
    seed: String,
    private val multicastLock: MulticastLease.Lock?,
) : MachineSession {

    private val session = Session(machine.deviceId, seed).apply {
        setTimeoutSeconds(CONNECT_TIMEOUT_SECONDS)
    }

    // The lock covers the whole dial, including its retries: the
    // local-discovery leg races the relay for as long as connect()
    // runs, and hears nothing without it. Released either way, so a
    // machine that never answers does not leave it held.
    override fun connect() = classified(session::lastFailure, session::lastFailureDetail) {
        MulticastLease.holding(multicastLock) { session.connect() }
    }
    override fun cancel() = session.cancel()
    override fun close() = session.close()
    override fun decline(reason: String) = session.decline(reason)
    override fun machineName(): String = session.machineName()
    override fun peerId(): String = session.peerID()
    override fun confirmCodeChallenge(): String = session.confirmCodeChallenge()
    override fun hasRequest() = session.hasRequest()
    override fun requestKidHex() = session.requestKid().toHex()
    override fun submitConfirmCode(code: String) = classified(session::lastFailure, session::lastFailureDetail) {
        session.submitConfirmCode(code)
    }

    override fun answer(agree: (ByteArray, ByteArray) -> ByteArray) {
        // Not classified as a whole: agree's own failures (an expired
        // or invalidated key) must reach the flow as themselves.
        val xOnly = agree(session.requestPointAffineX(), session.requestPointAffineY())
        classified(session::lastFailure, session::lastFailureDetail) { session.answerXOnly(xOnly) }
    }

    private companion object {
        const val CONNECT_TIMEOUT_SECONDS = 90L
    }
}

/** A [PairingAttempt] over the gomobile `Pairer`. */
private class GoPairing(
    private val seed: String,
    private val multicastLock: MulticastLease.Lock?,
) : PairingAttempt {

    private val pairer: Pairer = Mobile.newPairer()

    // Local discovery needs the lock just as an unlock does: pairing
    // dials over the same two transports.
    override fun pair(machine: Machine, code: String, identity: Identity) =
        classified(pairer::lastFailure, pairer::lastFailureDetail, FailureKind.PAIR_UNREACHABLE) {
            MulticastLease.holding(multicastLock) {
                pairer.pair(
                    machine.deviceId,
                    code,
                    seed,
                    identity.publicKeyHex,
                    android.os.Build.MODEL ?: "phone",
                    "",
                    PAIR_TIMEOUT_SECONDS,
                )
            }
        }

    override fun cancel() = pairer.cancel()

    private companion object {
        // Shorter than a connect: the machine is showing a code that
        // expires in minutes and somebody is standing in front of it,
        // so a pairing that has not happened by now has gone wrong and
        // saying so beats a longer wait.
        const val PAIR_TIMEOUT_SECONDS = 60L
    }
}

internal const val LOG_TAG = "synctang"

/**
 * gomobile turns every Go error into a plain Exception carrying only
 * its text, so the Go objects report the kind of their last failure
 * separately; this reattaches it. [unreachable] is the kind to use for
 * an unreachable machine, which means something different when pairing.
 */
private inline fun <T> classified(
    lastFailure: () -> Long,
    lastDetail: () -> String,
    unreachable: FailureKind = FailureKind.UNREACHABLE,
    call: () -> T,
): T = try {
    call()
} catch (e: Exception) {
    val kind = when (lastFailure()) {
        Mobile.FailureCancelled -> FailureKind.CANCELLED
        Mobile.FailureUnreachable -> unreachable
        Mobile.FailureHungUp -> FailureKind.HUNG_UP
        Mobile.FailureNoReply -> FailureKind.NO_REPLY
        Mobile.FailureOutcomeUnknown -> FailureKind.OUTCOME_UNKNOWN
        Mobile.FailureAnswerRejected -> FailureKind.ANSWER_REJECTED
        Mobile.FailureBadPairingCode -> FailureKind.BAD_PAIRING_CODE
        Mobile.FailurePairCodeMismatch -> FailureKind.PAIR_CODE_MISMATCH
        Mobile.FailurePairDeclined -> FailureKind.PAIR_DECLINED
        Mobile.FailurePairRefused -> FailureKind.PAIR_REFUSED
        Mobile.FailureProtocol -> FailureKind.PROTOCOL
        else -> FailureKind.OTHER
    }
    throw MachineFailure(kind, lastDetail(), e.message, e)
}
