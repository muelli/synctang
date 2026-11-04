// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.platform.app.InstrumentationRegistry
import com.github.muelli.synctang.ui.AppBar
import com.github.muelli.synctang.ui.Busy
import com.github.muelli.synctang.ui.DoneScreen
import com.github.muelli.synctang.ui.FailedScreen
import com.github.muelli.synctang.ui.PairingScreen
import com.github.muelli.synctang.ui.ReadyScreen
import com.github.muelli.synctang.ui.ScreenColumn
import com.github.muelli.synctang.ui.SynctangTheme
import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

/**
 * Renders the screens the F-Droid listing shows, from sample data, and writes each as a
 * PNG, 1.png to 5.png in the app's external files directory under "screenshots". Not a
 * test of behaviour: it passes when the pictures were made. scripts/screenshots.sh runs it
 * on an emulator and pulls the files into the fastlane metadata.
 *
 * Composed and drawn from the test, so no sleeping screen, keyguard or status bar gets in the
 * way, and no real machine, key or biometric is involved.
 */
class ScreenshotsTest {

    @get:Rule
    val rule = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val out = File(context.getExternalFilesDir(null), "screenshots").apply { mkdirs() }

    private val machine = Machine("MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ", "study workstation")
    private val identity = Identity(
        "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MFZWI3D-BONSGYC-YLTMRWG-C43ENR5",
        "04" + "a1b2c3d4e5f60718293a4b5c6d7e8f90".repeat(4),
        KeyHolderKey.Backing.TEE,
    )

    @Composable
    private fun Frame(aboutOpen: Boolean = false, content: @Composable () -> Unit) {
        SynctangTheme {
            Surface(Modifier.fillMaxSize()) {
                Column {
                    AppBar(aboutOpen, onOpenAbout = {}, onCloseAbout = {}, appName = "synctang")
                    ScreenColumn { content() }
                }
            }
        }
    }

    private fun shoot(n: Int, content: @Composable () -> Unit) {
        // A spinner is drawn as the arc of the moment. Held at 800 ms for the composition and
        // then let go, so the capture a few frames later catches nearly the same arc every run
        // and not the first frame, where it is a speck. The window cannot be copied while the
        // clock is stopped, hence letting go before the capture.
        rule.mainClock.autoAdvance = false
        rule.setContent { content() }
        rule.mainClock.advanceTimeBy(800)
        rule.mainClock.autoAdvance = true
        rule.waitForIdle()
        val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
        val file = File(out, "$n.png")
        file.outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        assertTrue("nothing was written to $file", file.length() > 0)
    }

    @Test
    fun readyAndWaiting() = shoot(1) {
        Frame {
            ReadyScreen(
                UnlockUiState.Ready(machine, identity, readiness = Readiness(ReadinessState.WAITING, 12)),
                {}, {}, {},
            )
        }
    }

    @Test
    fun connecting() = shoot(2) {
        Frame {
            Busy(
                context.getString(R.string.unlock_connecting),
                machine,
                Progress(Stage.WAITING_FOR_REQUEST, attempt = 1, route = Route.RELAY, startedAtMillis = System.currentTimeMillis() - 7_000),
                onCancel = {},
            )
        }
    }

    @Test
    fun unlocked() = shoot(3) { Frame { DoneScreen(UnlockUiState.Done(machine), {}) } }

    @Test
    fun failedWithWhatDiscoveryKnows() = shoot(4) {
        Frame {
            FailedScreen(
                UnlockUiState.Failed(machine, context.getString(R.string.failure_unreachable_stale, "4 min")),
                {}, {},
            )
        }
    }

    @Test
    fun pairing() = shoot(5) {
        Frame {
            PairingScreen(
                UnlockUiState.NeedsPairing(identity), { true }, {}, {},
            ) { _, modifier -> CameraPermissionPrompt(CameraAccess.ASK, {}, {}, modifier) }
        }
    }
}
