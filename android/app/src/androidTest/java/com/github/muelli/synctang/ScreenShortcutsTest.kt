// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.view.KeyEvent
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.AboutScreen
import com.github.muelli.synctang.ui.AppAction
import com.github.muelli.synctang.ui.AppBar
import com.github.muelli.synctang.ui.ApproveScreen
import com.github.muelli.synctang.ui.Busy
import com.github.muelli.synctang.ui.DoneScreen
import com.github.muelli.synctang.ui.FailedScreen
import com.github.muelli.synctang.ui.KeyNeedsReplacingScreen
import com.github.muelli.synctang.ui.LocalShortcuts
import com.github.muelli.synctang.ui.PairingScreen
import com.github.muelli.synctang.ui.ReadyScreen
import com.github.muelli.synctang.ui.ScreenColumn
import com.github.muelli.synctang.ui.ShortcutAction
import com.github.muelli.synctang.ui.ShortcutRegistry
import com.github.muelli.synctang.ui.SoftwareKeyScreen
import com.github.muelli.synctang.ui.SynctangTheme
import com.github.muelli.synctang.ui.UndoSnackbarHost
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.launch
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test

/**
 * Every control on every screen has a key, and the key does what
 * pressing the control does. The screens are shown on their own with
 * callbacks that count, so that what is checked is the wiring: that no
 * button was added without a shortcut, and that none points at the
 * wrong action.
 */
class ScreenShortcutsTest {

    @get:Rule
    val rule = createComposeRule()

    private val registry = ShortcutRegistry()
    private val longId = "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ"
    private val machine = Machine(longId, "workstation")
    private val identity = Identity(longId, "04" + "ab".repeat(64), KeyHolderKey.Backing.TEE)
    private val calls = mutableListOf<String>()

    @Before
    fun leaveTouchMode() {
        InstrumentationRegistry.getInstrumentation().setInTouchMode(false)
    }

    private fun show(content: @Composable () -> Unit) {
        rule.setContent {
            CompositionLocalProvider(LocalShortcuts provides registry) {
                SynctangTheme { ScreenColumn { content() } }
            }
        }
        rule.waitForIdle()
    }

    private fun press(action: AppAction) {
        val chord = action.chord!!
        rule.runOnUiThread {
            assertTrue(
                "$action was not handled",
                registry.handle(chord.keyCode, chord.ctrl, chord.shift, alt = false, meta = false),
            )
        }
        rule.waitForIdle()
    }

    /**
     * Every control that can be pressed carries an action, and the screen offers exactly [expected].
     * A text field is typed into, not pressed, so it is not one of them.
     */
    private fun assertOffers(vararg expected: AppAction) {
        val unbound = rule.onAllNodes(hasClickAction() and !hasSetTextAction()).fetchSemanticsNodes()
            .filter { it.config.getOrNull(ShortcutAction) == null }
        assertTrue("controls without a shortcut: ${unbound.map { it.config }}", unbound.isEmpty())
        assertEquals(expected.toList(), registry.available)
    }

    @Test
    fun ready() {
        show { ReadyScreen(UnlockUiState.Ready(machine, identity), { calls += "unlock" }, { calls += "again" }) }
        assertOffers(AppAction.UNLOCK, AppAction.PAIR_AGAIN)
        press(AppAction.UNLOCK)
        press(AppAction.PAIR_AGAIN)
        assertEquals(listOf("unlock", "again"), calls)
    }

    @Test
    fun approve() {
        show {
            ApproveScreen(
                UnlockUiState.Approve(machine, longId, "ab".repeat(32)),
                { calls += "approve" },
                { calls += "decline" },
            )
        }
        assertOffers(AppAction.APPROVE, AppAction.DECLINE, AppAction.TOGGLE_DEVICE_ID, AppAction.TOGGLE_KEY_ID)
        press(AppAction.APPROVE)
        press(AppAction.DECLINE)
        assertEquals(listOf("approve", "decline"), calls)

        // Each toggle widens its own identifier and no other.
        rule.onNodeWithText(longId, substring = true).assertDoesNotExist()
        press(AppAction.TOGGLE_DEVICE_ID)
        rule.onNodeWithText(longId).assertExists()
        press(AppAction.TOGGLE_DEVICE_ID)
        rule.onNodeWithText(longId).assertDoesNotExist()
    }

    @Test
    fun done() {
        show { DoneScreen(UnlockUiState.Done(machine)) { calls += "done" } }
        assertOffers(AppAction.UNLOCK_AGAIN)
        press(AppAction.UNLOCK_AGAIN)
        assertEquals(listOf("done"), calls)
    }

    @Test
    fun busyOffersCancelOnlyWhereItCanBeCancelled() {
        show { Busy("working", machine) { calls += "cancel" } }
        assertOffers(AppAction.CANCEL)
        press(AppAction.CANCEL)
        assertEquals(listOf("cancel"), calls)
    }

    @Test
    fun workingOffersNothing() {
        show { Busy("working", machine) }
        assertOffers()
    }

    @Test
    fun failedToTryAgain() {
        show {
            FailedScreen(UnlockUiState.Failed(machine, "no", technical = "boom"), { calls += "retry" }, { calls += "scan" })
        }
        assertOffers(AppAction.RETRY, AppAction.TOGGLE_DETAILS)
        press(AppAction.RETRY)
        rule.onNodeWithText("boom").assertDoesNotExist()
        press(AppAction.TOGGLE_DETAILS)
        rule.onNodeWithText("boom").assertExists()
        assertEquals(listOf("retry"), calls)
    }

    @Test
    fun failedPairingScansAgain() {
        show {
            FailedScreen(UnlockUiState.Failed(machine, "no", Retry.SCAN_AGAIN), { calls += "retry" }, { calls += "scan" })
        }
        assertOffers(AppAction.SCAN_AGAIN)
        press(AppAction.SCAN_AGAIN)
        assertEquals(listOf("scan"), calls)
    }

    @Test
    fun pairingOffersPairOnlyOnceSomethingIsTyped() {
        show {
            PairingScreen(
                UnlockUiState.NeedsPairing(identity, previous = machine),
                { calls += "pair:$it"; true },
                { calls += "keep" },
                { calls += "by-hand" },
            ) { _, modifier -> CameraPermissionPrompt(CameraAccess.ASK, { calls += "camera" }, {}, modifier) }
        }
        assertOffers(AppAction.KEEP_CURRENT, AppAction.ENROL_BY_HAND, AppAction.GRANT_CAMERA)

        rule.onNodeWithText("Pairing link or machine device ID").performTextInput("abc")
        rule.waitForIdle()
        assertOffers(AppAction.PAIR, AppAction.KEEP_CURRENT, AppAction.ENROL_BY_HAND, AppAction.GRANT_CAMERA)

        press(AppAction.PAIR)
        press(AppAction.KEEP_CURRENT)
        press(AppAction.ENROL_BY_HAND)
        press(AppAction.GRANT_CAMERA)
        assertEquals(listOf("pair:abc", "keep", "by-hand", "camera"), calls)
    }

    @Test
    fun pairingWithCameraDeniedOffersSettings() {
        show {
            PairingScreen(UnlockUiState.NeedsPairing(identity), { true }, {}, {}) { _, modifier ->
                CameraPermissionPrompt(CameraAccess.OPEN_SETTINGS, {}, { calls += "settings" }, modifier)
            }
        }
        assertOffers(AppAction.ENROL_BY_HAND, AppAction.OPEN_SETTINGS)
        press(AppAction.OPEN_SETTINGS)
        assertEquals(listOf("settings"), calls)
    }

    @Test
    fun softwareKeyHasNoKeyButIsStillReachable() {
        show { SoftwareKeyScreen { calls += "allow" } }
        val bound = rule.onAllNodes(hasClickAction()).fetchSemanticsNodes()
            .map { it.config.getOrNull(ShortcutAction) }
        assertEquals(listOf(AppAction.ALLOW_SOFTWARE_KEY), bound)
        assertEquals(emptyList<AppAction>(), registry.available.filter { it.chord != null })
        assertTrue(calls.isEmpty())
    }

    @Test
    fun aKeyThatIsNotWholeOffersTryingAgainByKeyAndMakingANewOneOnlyByHand() {
        show { KeyNeedsReplacingScreen(KeyProblem.FILES_DAMAGED, { calls += "retry" }, { calls += "replace" }) }
        assertOffers(AppAction.RETRY, AppAction.REPLACE_KEY)
        press(AppAction.RETRY)
        assertEquals(listOf("retry"), calls)
        assertEquals(null, AppAction.REPLACE_KEY.chord)
        rule.onNodeWithText("Make a new key").performClick()
        assertEquals(listOf("retry", "replace"), calls)
    }

    @Test
    fun about() {
        show { AboutScreen(identity, "1") }
        assertOffers(AppAction.COPY_COMMAND)
    }

    @Test
    fun appBarOffersAboutOrBack() {
        var open by mutableStateOf(false)
        rule.setContent {
            CompositionLocalProvider(LocalShortcuts provides registry) {
                SynctangTheme { AppBar(open, { calls += "about" }, { calls += "close" }) }
            }
        }
        rule.waitForIdle()
        assertOffers(AppAction.OPEN_ABOUT)
        press(AppAction.OPEN_ABOUT)
        rule.runOnUiThread { open = true }
        rule.waitForIdle()
        assertOffers(AppAction.CLOSE_ABOUT)
        press(AppAction.CLOSE_ABOUT)
        assertEquals(listOf("about", "close"), calls)
    }

    @Test
    fun undoBarOffersUndoAndDismiss() {
        val host = SnackbarHostState()
        rule.setContent {
            CompositionLocalProvider(LocalShortcuts provides registry) {
                SynctangTheme { UndoSnackbarHost(host) }
            }
        }
        rule.runOnUiThread {
            MainScope().launch {
                host.showSnackbar("Now paired", actionLabel = "Undo", withDismissAction = true)
            }
        }
        rule.waitForIdle()
        assertOffers(AppAction.UNDO, AppAction.DISMISS)
    }

    /** The first screen to take focus is the one a keyboard user is about to use. */
    @Test
    fun readyStartsOnUnlock() {
        show { ReadyScreen(UnlockUiState.Ready(machine, identity), {}, {}) }
        rule.onNodeWithText("Unlock").assertIsFocused()
    }
}
