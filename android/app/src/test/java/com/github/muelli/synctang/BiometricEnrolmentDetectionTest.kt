// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.security.InvalidAlgorithmParameterException
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A phone with no fingerprint or face enrolled cannot create the
 * key this app is built around, and that is a thing the person
 * holding it can fix in Settings in about a minute. It only helps if
 * the app says so.
 *
 * It did not. The Keystore does not raise the useful
 * IllegalStateException ("At least one biometric must be enrolled
 * ...") to the caller; it raises an
 * InvalidAlgorithmParameterException *wrapping* it. The code caught
 * the inner type, so the mapping never ran and the first screen of a
 * fresh install was the raw exception string with a "Try again"
 * button that could only ever fail again. Seen on an emulator with no
 * biometric enrolled, which is exactly the state a new test device is
 * in.
 *
 * Matching on the cause chain's type rather than on the message text,
 * because that message is Android's wording and not a promise.
 */
class BiometricEnrolmentDetectionTest {

    @Test
    fun seesThroughTheWrapperTheKeystoreActuallyThrows() {
        val thrown = InvalidAlgorithmParameterException(
            IllegalStateException(
                "At least one biometric must be enrolled to create keys " +
                    "requiring user authentication for every use",
            ),
        )
        assertTrue(thrown.looksLikeMissingBiometricEnrolment())
    }

    @Test
    fun stillRecognisesTheUnwrappedForm() {
        assertTrue(IllegalStateException("whatever").looksLikeMissingBiometricEnrolment())
    }

    @Test
    fun doesNotClaimEveryFailureIsAMissingBiometric() {
        assertFalse(
            InvalidAlgorithmParameterException("unsupported curve").looksLikeMissingBiometricEnrolment(),
        )
    }

    @Test
    fun doesNotLoopForeverOnACycleInTheCauseChain() {
        val a = IllegalArgumentException("a")
        val b = IllegalArgumentException("b", a)
        a.initCause(b)
        assertFalse(a.looksLikeMissingBiometricEnrolment())
    }
}
