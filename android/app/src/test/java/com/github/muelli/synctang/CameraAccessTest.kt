// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Once Android has decided not to show the camera permission dialog
 * again, asking for the permission returns "denied" at once with no
 * dialog. The "Allow the camera" button then did nothing at all, with
 * nothing on screen to say why. The only way on from there is the
 * app's page in Settings, so that is what the screen has to offer.
 *
 * Android does not say "permanently denied" outright: a refusal with
 * no rationale to show is how it looks.
 */
class CameraAccessTest {

    @Test
    fun aGrantedPermissionShowsTheCamera() {
        assertEquals(CameraAccess.GRANTED, cameraAccessAfterRequest(granted = true, showRationale = false))
        assertEquals(CameraAccess.GRANTED, initialCameraAccess(hasPermission = true))
    }

    @Test
    fun aFirstRefusalCanBeAskedAgain() {
        assertEquals(CameraAccess.ASK, cameraAccessAfterRequest(granted = false, showRationale = true))
    }

    @Test
    fun aRefusalWithNoDialogLeftSendsThePersonToSettings() {
        assertEquals(CameraAccess.OPEN_SETTINGS, cameraAccessAfterRequest(granted = false, showRationale = false))
    }

    @Test
    fun noPermissionYetStartsByAsking() {
        assertEquals(CameraAccess.ASK, initialCameraAccess(hasPermission = false))
    }
}
