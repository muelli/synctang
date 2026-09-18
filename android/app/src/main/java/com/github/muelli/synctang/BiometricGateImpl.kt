// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import kotlin.coroutines.resume
import kotlinx.coroutines.suspendCancellableCoroutine

/**
 * The real biometric prompt.
 *
 * Note what this does NOT do: it does not pass a
 * BiometricPrompt.CryptoObject. There is no CryptoObject constructor
 * for a KeyAgreement on any Android release up to and including API 34
 * (the constructors are Signature, Cipher, Mac, IdentityCredential and
 * PresentationSession), so a key with PURPOSE_AGREE_KEY cannot be
 * bound to a prompt operation the way a Cipher key can.
 *
 * The gate is therefore enforced by the Keystore rather than by this
 * prompt: the key is created with a short authentication validity
 * window and AUTH_BIOMETRIC_STRONG (see KeyHolderKey), so Keymint
 * refuses the ECDH unless a strong biometric matched within that
 * window. This prompt is how the window gets opened; it is not the
 * thing an attacker could skip to avoid the check, because skipping it
 * leaves the key unusable.
 */
class BiometricGateImpl(private val activity: FragmentActivity) : BiometricGate {

    override suspend fun authenticate(title: String, subtitle: String): AuthOutcome {
        val manager = BiometricManager.from(activity)
        val can = manager.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG)
        if (can != BiometricManager.BIOMETRIC_SUCCESS) {
            return AuthOutcome.UNAVAILABLE
        }

        return suspendCancellableCoroutine { continuation ->
            val prompt = BiometricPrompt(
                activity,
                ContextCompat.getMainExecutor(activity),
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                        if (continuation.isActive) continuation.resume(AuthOutcome.SUCCEEDED)
                    }

                    override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                        if (continuation.isActive) continuation.resume(AuthOutcome.CANCELLED)
                    }

                    // A single non-matching finger is not a decision:
                    // the prompt stays up and the user tries again.
                    override fun onAuthenticationFailed() = Unit
                },
            )

            val info = BiometricPrompt.PromptInfo.Builder()
                .setTitle(title)
                .setSubtitle(subtitle)
                .setNegativeButtonText(activity.getString(R.string.biometric_cancel))
                .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
                .build()

            continuation.invokeOnCancellation { prompt.cancelAuthentication() }
            prompt.authenticate(info)
        }
    }
}
