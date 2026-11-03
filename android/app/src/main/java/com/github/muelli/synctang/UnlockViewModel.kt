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
 * gate on a biometric, unwrap the key with the Keystore, answer.
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

    // The Go side's log: everything in a debug build, so it can be followed from
    // `adb logcat -s synctang GoLog`, and warnings only in a release build.
    init {
        Mobile.setLogLevel(if (BuildConfig.DEBUG) "trace" else "warn")
    }

    private val store = PairingStore(application)
    // Alias and gating both come from TestMode, together: an ungated
    // key lives under its own alias so it can never be mistaken for
    // the real one.
    // The wrapped key goes in the directory Android does not back up: a restore
    // onto another phone could only bring back a file nothing there can unwrap.
    private val key = KeyHolderKey(
        directory = application.noBackupFilesDir,
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
        newSession = { machine -> GoSession(machine, store.identitySeed(), multicastLock, key::publicPoint) },
        newPairing = { GoPairing(store.identitySeed(), multicastLock) },
        prepareKey = key::prepareUnwrap,
        unwrapKey = key::unwrap,
        checkKey = { key.prepareUnwrap() },
        resetIdentity = ::resetIdentity,
        allowSoftwareKey = store::allowSoftwareKey,
        strings = object : Strings {
            override fun get(id: Int, vararg args: Any) = application.getString(id, *args)
        },
        appLog = AppLog.logcat,
        checkStatus = { machine ->
            // One lookup on the machine's discovery server; no connection to the machine.
            val status = Mobile.checkMachineStatus(machine.deviceId, machine.discovery)
            if (status.state == GO_RATE_LIMITED) {
                StatusAnswer.RateLimited(rateLimitWaitMillis(status.retryAfterSeconds.toInt()))
            } else {
                StatusAnswer.Known(Readiness(ReadinessState.fromGo(status.state), status.ageSeconds.toInt()))
            }
        },
    )

    val state: StateFlow<UnlockUiState> = flow.state

    init {
        refresh()
    }

    private fun identity(): Identity {
        val created = !key.exists()
        val backing = key.ensureKey()
        // The debug build with no biometric gate is for emulators, whose
        // Keystore is software; it has no secrets to protect.
        if (KeyHolderKey.refuses(backing, softwareAllowed = store.softwareKeyAllowed() || TestMode.noBiometric)) {
            // A key made a moment ago has nothing enrolled against it, so
            // do not leave it behind. One that already existed may be what
            // machines are enrolled on: it is left alone, and the person
            // decides.
            if (created) key.delete()
            throw SoftwareKeyRefused()
        }
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
    fun returnedToForeground() = flow.returnedToForeground()
    fun movedToBackground() = flow.movedToBackground()
    fun checkAgain() = flow.checkAgain()
    fun startPairing() = flow.startPairing()
    fun keepCurrentMachine() = flow.keepCurrentMachine()
    fun allowSoftwareKey() = flow.allowSoftwareKey()
    fun replaceKey() = flow.replaceKey()
    fun undoReplace() = flow.undoReplace()
    fun dismissUndo() = flow.dismissUndo()
    fun pair(scanned: String): Boolean = flow.pair(scanned)
    fun connect() = flow.connect()
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
    // The public point this phone was enrolled with, read when asked: the Go side
    // checks the scalar it is given against it.
    private val publicPoint: () -> ByteArray,
) : MachineSession {

    private val session = Session(machine.deviceId, seed).apply {
        setTimeoutSeconds(CONNECT_TIMEOUT_SECONDS)
        // The machine's own servers, if its owner runs them: it is looked up where it announces.
        setDiscovery(machine.discovery)
    }

    // The lock covers the whole dial, including its retries: the
    // local-discovery leg races the relay for as long as connect()
    // runs, and hears nothing without it. Released either way, so a
    // machine that never answers does not leave it held.
    override fun connect() = classified(session::lastFailure, session::lastFailureDetail) {
        MulticastLease.holding(multicastLock) { session.connect() }
    }
    // Plain getters on the Go side that never wait for a call in progress, so this is safe
    // to read from the main thread while connect() or answer() runs on another.
    override fun progress() = Progress(
        Stage.fromGo(session.stage()),
        session.attempt().toInt(),
        Route.fromGo(session.route()),
    )

    override fun connectedForMillis(): Long = session.connectedForMillis()

    override fun cancel() = session.cancel()
    override fun close() = session.close()
    override fun decline(reason: String) = session.decline(reason)
    override fun machineName(): String = session.machineName()
    override fun peerId(): String = session.peerID()
    override fun hasRequest() = session.hasRequest()
    override fun requestKidHex() = session.requestKid().toHex()

    override fun answer(unwrap: () -> ByteArray) {
        // Not classified as a whole: the unwrapping's own failures (a retired key, a
        // Keystore that does not answer) must reach the flow as themselves.
        val scalar = unwrap()
        try {
            // The Go side multiplies the machine's challenge point by the scalar and
            // wipes its copy; this wipes the app's, whatever happens.
            classified(session::lastFailure, session::lastFailureDetail) { session.answerWithScalar(scalar, publicPoint()) }
        } finally {
            scalar.fill(0)
        }
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
                pairer.setDiscovery(machine.discovery)
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

    override fun progress() = Progress(
        Stage.fromGo(pairer.stage()),
        pairer.attempt().toInt(),
        Route.fromGo(pairer.route()),
    )

    override fun cancel() = pairer.cancel()

    private companion object {
        // How long to try to reach the machine and send it the proof: shorter
        // than a connect, since the machine is showing a code that expires in
        // minutes and somebody is standing in front of it. It does not bound the
        // wait for the person at the machine to confirm, which the Go side gives
        // five minutes of its own, so a slow decision is not mistaken for a
        // machine that could not be reached.
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
        Mobile.FailureKeyMismatch -> FailureKind.KEY_MISMATCH
        Mobile.FailurePairNotConfirmed -> FailureKind.PAIR_NOT_CONFIRMED
        else -> FailureKind.OTHER
    }
    throw MachineFailure(kind, lastDetail(), e.message, e)
}
