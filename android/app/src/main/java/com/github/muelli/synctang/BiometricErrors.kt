// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt

/**
 * Maps a BiometricPrompt error code to what actually happened. Only an
 * explicit dismissal is a cancellation: a lockout is the phone
 * refusing, not the user changing their mind, and anything unrecognised
 * is treated as the hardware being unusable rather than as a choice.
 */
fun authOutcomeForError(errorCode: Int): AuthOutcome = when (errorCode) {
    BiometricPrompt.ERROR_USER_CANCELED,
    BiometricPrompt.ERROR_NEGATIVE_BUTTON,
    BiometricPrompt.ERROR_CANCELED -> AuthOutcome.CANCELLED
    BiometricPrompt.ERROR_LOCKOUT -> AuthOutcome.LOCKED_OUT
    BiometricPrompt.ERROR_LOCKOUT_PERMANENT -> AuthOutcome.LOCKED_OUT_PERMANENTLY
    BiometricPrompt.ERROR_NO_BIOMETRICS -> AuthOutcome.NO_BIOMETRIC
    else -> AuthOutcome.UNAVAILABLE
}

/**
 * Maps the result of BiometricManager.canAuthenticate: null means the
 * prompt can be shown. Only "none enrolled" is something the user can
 * fix in Settings; any other failure is the hardware, and telling the
 * user to enrol a fingerprint would be untrue.
 */
fun authOutcomeForCapability(canAuthenticate: Int): AuthOutcome? = when (canAuthenticate) {
    BiometricManager.BIOMETRIC_SUCCESS -> null
    BiometricManager.BIOMETRIC_ERROR_NONE_ENROLLED -> AuthOutcome.NO_BIOMETRIC
    else -> AuthOutcome.UNAVAILABLE
}

/**
 * The message for an outcome that stopped the unlock. Each is distinct
 * because each asks the user to do something different.
 */
@StringRes
fun failureMessage(outcome: AuthOutcome): Int = when (outcome) {
    AuthOutcome.CANCELLED -> R.string.error_cancelled
    AuthOutcome.LOCKED_OUT -> R.string.error_biometric_lockout
    AuthOutcome.LOCKED_OUT_PERMANENTLY -> R.string.error_biometric_lockout_permanent
    AuthOutcome.NO_BIOMETRIC -> R.string.error_no_biometric
    AuthOutcome.UNAVAILABLE -> R.string.error_biometric_unavailable
    AuthOutcome.SUCCEEDED -> throw IllegalArgumentException("a successful outcome has no failure message")
}
