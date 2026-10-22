// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The screen after a failed biometric prompt must say what happened.
 *
 * It did not. Every error code from the prompt was mapped to
 * "cancelled", so a phone whose sensor had locked itself after too
 * many wrong fingers, or whose biometric hardware had faulted, told
 * the user "Cancelled. Nothing was sent to the machine." They had not
 * cancelled anything, and retrying at once could not help. Likewise a
 * hardware fault found before the prompt was shown reported that no
 * biometric was enrolled, sending the user to Settings to fix
 * something that was not broken.
 *
 * Only an explicit dismissal is a cancellation. Lockout needs waiting
 * (or the device credential, when permanent), enrolment needs
 * Settings, and anything else is the hardware being unusable.
 */
class BiometricErrorsTest {

    @Test
    fun userCanceledIsCancelled() {
        assertEquals(AuthOutcome.CANCELLED, authOutcomeForError(BiometricPrompt.ERROR_USER_CANCELED))
    }

    @Test
    fun negativeButtonIsCancelled() {
        assertEquals(AuthOutcome.CANCELLED, authOutcomeForError(BiometricPrompt.ERROR_NEGATIVE_BUTTON))
    }

    @Test
    fun systemCanceledIsCancelled() {
        assertEquals(AuthOutcome.CANCELLED, authOutcomeForError(BiometricPrompt.ERROR_CANCELED))
    }

    @Test
    fun lockoutIsLockedOut() {
        assertEquals(AuthOutcome.LOCKED_OUT, authOutcomeForError(BiometricPrompt.ERROR_LOCKOUT))
    }

    @Test
    fun permanentLockoutIsLockedOutPermanently() {
        assertEquals(
            AuthOutcome.LOCKED_OUT_PERMANENTLY,
            authOutcomeForError(BiometricPrompt.ERROR_LOCKOUT_PERMANENT),
        )
    }

    @Test
    fun lockoutIsNeverCancelled() {
        assertNotEquals(AuthOutcome.CANCELLED, authOutcomeForError(BiometricPrompt.ERROR_LOCKOUT))
        assertNotEquals(AuthOutcome.CANCELLED, authOutcomeForError(BiometricPrompt.ERROR_LOCKOUT_PERMANENT))
    }

    @Test
    fun noBiometricsIsNoBiometric() {
        assertEquals(AuthOutcome.NO_BIOMETRIC, authOutcomeForError(BiometricPrompt.ERROR_NO_BIOMETRICS))
    }

    @Test
    fun hardwareFaultsAreUnavailable() {
        assertEquals(AuthOutcome.UNAVAILABLE, authOutcomeForError(BiometricPrompt.ERROR_HW_UNAVAILABLE))
        assertEquals(AuthOutcome.UNAVAILABLE, authOutcomeForError(BiometricPrompt.ERROR_HW_NOT_PRESENT))
        assertEquals(AuthOutcome.UNAVAILABLE, authOutcomeForError(BiometricPrompt.ERROR_NO_SPACE))
        assertEquals(AuthOutcome.UNAVAILABLE, authOutcomeForError(BiometricPrompt.ERROR_TIMEOUT))
    }

    @Test
    fun unknownCodeIsUnavailable() {
        assertEquals(AuthOutcome.UNAVAILABLE, authOutcomeForError(9999))
    }

    @Test
    fun capabilitySuccessGoesOnToThePrompt() {
        assertNull(authOutcomeForCapability(BiometricManager.BIOMETRIC_SUCCESS))
    }

    @Test
    fun capabilityNoneEnrolledIsNoBiometric() {
        assertEquals(
            AuthOutcome.NO_BIOMETRIC,
            authOutcomeForCapability(BiometricManager.BIOMETRIC_ERROR_NONE_ENROLLED),
        )
    }

    @Test
    fun capabilityHardwareProblemsAreUnavailable() {
        assertEquals(
            AuthOutcome.UNAVAILABLE,
            authOutcomeForCapability(BiometricManager.BIOMETRIC_ERROR_HW_UNAVAILABLE),
        )
        assertEquals(
            AuthOutcome.UNAVAILABLE,
            authOutcomeForCapability(BiometricManager.BIOMETRIC_ERROR_NO_HARDWARE),
        )
    }

    @Test
    fun messagesMatchTheirOutcome() {
        assertEquals(R.string.error_cancelled, failureMessage(AuthOutcome.CANCELLED))
        assertEquals(R.string.error_biometric_lockout, failureMessage(AuthOutcome.LOCKED_OUT))
        assertEquals(
            R.string.error_biometric_lockout_permanent,
            failureMessage(AuthOutcome.LOCKED_OUT_PERMANENTLY),
        )
        assertEquals(R.string.error_biometric_unavailable, failureMessage(AuthOutcome.UNAVAILABLE))
        assertEquals(R.string.error_no_biometric, failureMessage(AuthOutcome.NO_BIOMETRIC))
    }

    @Test
    fun everyFailureHasItsOwnMessage() {
        val ids = AuthOutcome.values()
            .filter { it != AuthOutcome.SUCCEEDED }
            .map { failureMessage(it) }
        assertEquals(5, ids.toSet().size)
    }
}
