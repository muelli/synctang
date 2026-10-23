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

    fun refresh() = flow.refresh()
    fun startPairing() = flow.startPairing()
    fun keepCurrentMachine() = flow.keepCurrentMachine()
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
    override fun connect() = MulticastLease.holding(multicastLock) { session.connect() }
    override fun cancel() = session.cancel()
    override fun close() = session.close()
    override fun decline(reason: String) = session.decline(reason)
    override fun machineName(): String = session.machineName()
    override fun peerId(): String = session.peerID()
    override fun confirmCodeChallenge(): String = session.confirmCodeChallenge()
    override fun hasRequest() = session.hasRequest()
    override fun requestKidHex() = session.requestKid().toHex()
    override fun submitConfirmCode(code: String) = session.submitConfirmCode(code)

    override fun answer(agree: (ByteArray, ByteArray) -> ByteArray) {
        val xOnly = agree(session.requestPointAffineX(), session.requestPointAffineY())
        session.answerXOnly(xOnly)
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

    override fun cancel() = pairer.cancel()

    private companion object {
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
