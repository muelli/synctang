// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.KeyProblem
import com.github.muelli.synctang.KeyHolderKey
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.R
import com.github.muelli.synctang.Retry
import com.github.muelli.synctang.UnlockUiState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A screen reader announces a screen by its title, and the task
 * switcher shows it, so each screen has to be told apart by name.
 */
class ScreenTitleTest {

    private val identity = Identity("PHONE", "04ab", KeyHolderKey.Backing.TEE)
    private val machine = Machine("MACHINE", "m")

    private val screens: List<UnlockUiState> = listOf(
        UnlockUiState.NeedsPairing(identity),
        UnlockUiState.Ready(machine, identity),
        UnlockUiState.Connecting(machine),
        UnlockUiState.Pairing(machine),
        UnlockUiState.Approve(machine, "PEER", "kid"),
        UnlockUiState.Working(machine),
        UnlockUiState.Done(machine),
        UnlockUiState.SoftwareKeyRefused,
        UnlockUiState.KeyNeedsReplacing(KeyProblem.FILES_DAMAGED),
        UnlockUiState.Failed(machine, "message", Retry.REFRESH),
    )

    @Test
    fun everyScreenHasItsOwnTitle() {
        val titles = screens.map { screenTitle(it, aboutOpen = false) }
        assertTrue(titles.all { it != 0 })
        assertEquals("two screens share a title: $titles", titles.size, titles.toSet().size)
    }

    @Test
    fun aboutIsNamedWhetherOrNotItCoversAnotherScreen() {
        for (screen in screens) {
            assertEquals(R.string.about_title, screenTitle(screen, aboutOpen = true))
        }
    }

    @Test
    fun theApprovalScreenIsNamedForWhatIsBeingAsked() {
        assertEquals(R.string.unlock_request_title, screenTitle(UnlockUiState.Approve(machine, "P", "k"), false))
        assertNotEquals(R.string.app_name, screenTitle(UnlockUiState.Ready(machine, identity), false))
    }
}
