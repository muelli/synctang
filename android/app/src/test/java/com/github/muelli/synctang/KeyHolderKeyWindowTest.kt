// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The Keystore refuses to use the key unless a strong biometric matched
 * within this many seconds, and the window is fixed when the key is
 * created. It is a time window, not a binding to the request on screen:
 * for as long as it is open, code running inside the app can use the key
 * without a prompt, and any fingerprint unlock of the phone opens it, not
 * only this app's prompt.
 *
 * So it has to be as short as the work between the prompt and the key use
 * allows, and only the local steps happen in between (the network write
 * comes after). This test is there so that nobody widens it again without
 * deciding to.
 */
class KeyHolderKeyWindowTest {

    @Test
    fun theWindowIsAFewSecondsAtMost() {
        assertTrue(
            "the authentication window is ${KeyHolderKey.AUTH_VALIDITY_SECONDS} s, which is longer than the local work between the prompt and the key use needs",
            KeyHolderKey.AUTH_VALIDITY_SECONDS <= 5,
        )
    }

    @Test
    fun theWindowIsNotSoShortThatTheKeystoreRefusesIt() {
        // Keymint takes whole seconds and a window of zero means a key that
        // needs a per-operation token, which a key agreement cannot get.
        assertTrue(KeyHolderKey.AUTH_VALIDITY_SECONDS >= 1)
    }
}
