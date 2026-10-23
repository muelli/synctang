// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.KeyHolderKey
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.UnlockUiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * About this phone is offered only where nothing is under way, so that
 * opening it can never hide a machine that is waiting for an answer.
 */
class AboutTest {

    private val identity = Identity("PHONE", "04ab", KeyHolderKey.Backing.STRONGBOX)
    private val machine = Machine("MACHINE", "m")

    @Test
    fun offeredWhereThePhonesIdentityIsAtHand() {
        assertEquals(identity, aboutIdentity(UnlockUiState.NeedsPairing(identity)))
        assertEquals(identity, aboutIdentity(UnlockUiState.NeedsPairing(identity, previous = machine)))
        assertEquals(identity, aboutIdentity(UnlockUiState.Ready(machine, identity)))
    }

    @Test
    fun notOfferedWhileAMachineIsWaitingOrWorkIsUnderWay() {
        val busy = listOf(
            UnlockUiState.Connecting(machine),
            UnlockUiState.Pairing(machine),
            UnlockUiState.Working(machine),
            UnlockUiState.ConfirmCode(machine, "MACHINE", "123456"),
            UnlockUiState.Approve(machine, "MACHINE", "ab"),
            UnlockUiState.Done(machine),
            UnlockUiState.Failed(machine, "failed"),
        )
        for (state in busy) assertNull("offered on $state", aboutIdentity(state))
    }
}
