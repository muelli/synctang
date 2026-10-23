// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.tooling.preview.Preview
import com.github.muelli.synctang.CameraAccess
import com.github.muelli.synctang.CameraPermissionPrompt
import com.github.muelli.synctang.Identity
import com.github.muelli.synctang.KeyHolderKey
import com.github.muelli.synctang.Machine
import com.github.muelli.synctang.R
import com.github.muelli.synctang.Retry
import com.github.muelli.synctang.UnlockUiState

// One preview per screen state, plus the awkward cases, so a layout
// change can be looked at without a phone. Sample data only.

private const val DEVICE_ID = "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ"
private const val PHONE_ID = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MFZWI3D-BONSGYC-YLTMRWG-C43ENR5"
private const val KID = "3f9a0c7be12d45a6f0e8b7c6d5a4b3c2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6"

private val machine = Machine(DEVICE_ID, "workstation")
private val longMachine = Machine(DEVICE_ID, "the-very-long-hostname-of-a-build-server.example.org")
private val identity = Identity(
    PHONE_ID,
    "04" + "a1b2c3d4e5f60718293a4b5c6d7e8f90".repeat(4),
    KeyHolderKey.Backing.STRONGBOX,
)

@Composable
private fun Frame(content: @Composable () -> Unit) {
    SynctangTheme {
        ScreenColumn { content() }
    }
}

private val noScanner: @Composable (onScanned: (String) -> Boolean, modifier: Modifier) -> Unit =
    { _, modifier -> CameraPermissionPrompt(CameraAccess.ASK, {}, {}, modifier) }

@Preview(name = "NeedsPairing", widthDp = 360, heightDp = 800)
@Composable
private fun NeedsPairingPreview() = Frame {
    PairingScreen(UnlockUiState.NeedsPairing(identity), { true }, {}, noScanner)
}

@Preview(name = "NeedsPairing, pairing again", widthDp = 360, heightDp = 800)
@Composable
private fun PairingAgainPreview() = Frame {
    PairingScreen(UnlockUiState.NeedsPairing(identity, previous = machine), { true }, {}, noScanner)
}

@Preview(name = "NeedsPairing, camera denied", widthDp = 360, heightDp = 800)
@Composable
private fun CameraDeniedPreview() = Frame {
    PairingScreen(UnlockUiState.NeedsPairing(identity), { true }, {}) { _, modifier ->
        CameraPermissionPrompt(CameraAccess.OPEN_SETTINGS, {}, {}, modifier)
    }
}

@Preview(name = "Ready", widthDp = 360, heightDp = 640)
@Composable
private fun ReadyPreview() = Frame {
    ReadyScreen(UnlockUiState.Ready(machine, identity), {}, {})
}

@Preview(name = "Ready, long machine name", widthDp = 360, heightDp = 640)
@Composable
private fun ReadyLongNamePreview() = Frame {
    ReadyScreen(UnlockUiState.Ready(longMachine, identity), {}, {})
}

@Preview(name = "Ready, large font", widthDp = 360, heightDp = 800, fontScale = 2f)
@Composable
private fun ReadyLargeFontPreview() = Frame {
    ReadyScreen(UnlockUiState.Ready(machine, identity), {}, {})
}

@Preview(name = "Connecting", widthDp = 360, heightDp = 640)
@Composable
private fun ConnectingPreview() = Frame {
    Busy(stringResource(R.string.unlock_connecting), onCancel = {})
}

@Preview(name = "Pairing", widthDp = 360, heightDp = 640)
@Composable
private fun PairingPreview() = Frame {
    Busy(stringResource(R.string.pair_working), onCancel = {})
}

@Preview(name = "Working", widthDp = 360, heightDp = 640)
@Composable
private fun WorkingPreview() = Frame {
    Busy(stringResource(R.string.unlock_working))
}

@Preview(name = "ConfirmCode", widthDp = 360, heightDp = 800)
@Composable
private fun ConfirmCodePreview() = Frame {
    ConfirmCodeScreen(UnlockUiState.ConfirmCode(machine, DEVICE_ID, "482913"), {}, {})
}

@Preview(name = "Approve", widthDp = 360, heightDp = 800)
@Composable
private fun ApprovePreview() = Frame {
    ApproveScreen(UnlockUiState.Approve(machine, DEVICE_ID, KID), {}, {})
}

@Preview(name = "Approve, large font, long name", widthDp = 360, heightDp = 1000, fontScale = 2f)
@Composable
private fun ApproveLargeFontPreview() = Frame {
    ApproveScreen(UnlockUiState.Approve(longMachine, DEVICE_ID, KID), {}, {})
}

@Preview(name = "Done", widthDp = 360, heightDp = 640)
@Composable
private fun DonePreview() = Frame {
    DoneScreen(UnlockUiState.Done(machine), {})
}

@Preview(name = "Failed", widthDp = 360, heightDp = 640)
@Composable
private fun FailedPreview() = Frame {
    FailedScreen(UnlockUiState.Failed(machine, stringResource(R.string.failure_unreachable)), {}, {})
}

@Preview(name = "Failed, long error", widthDp = 360, heightDp = 800)
@Composable
private fun FailedLongPreview() = Frame {
    FailedScreen(
        UnlockUiState.Failed(
            machine,
            stringResource(R.string.failure_other),
            detail = "mobile: gave up dialling $DEVICE_ID after 1m30s (last attempt: " +
                "relay: could not find a connectable relay; local discovery: no announcement heard)",
        ),
        {},
        {},
    )
}

@Preview(name = "Failed, pairing", widthDp = 360, heightDp = 640)
@Composable
private fun FailedPairingPreview() = Frame {
    FailedScreen(
        UnlockUiState.Failed(machine, stringResource(R.string.failure_pair_code_mismatch), Retry.SCAN_AGAIN),
        {},
        {},
    )
}
