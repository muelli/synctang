// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import javax.crypto.Cipher
import kotlin.coroutines.resume
import kotlinx.coroutines.suspendCancellableCoroutine

/**
 * The real biometric prompt.
 *
 * It is handed the cipher the key will be unwrapped with and passes it to the
 * prompt as its CryptoObject. That is what makes the fingerprint specific to this
 * one use: the secure hardware that holds the wrapping key will let this cipher,
 * and nothing else, decrypt once a strong biometric has matched, and not again
 * without another. Skipping the prompt does not help an attacker, because the
 * cipher is then simply refused; the prompt is not the check, it is how the hardware
 * is told the check passed.
 */
class BiometricGateImpl(private val activity: FragmentActivity) : BiometricGate {

    override suspend fun authenticate(title: String, subtitle: String, cipher: Cipher): AuthOutcome {
        val manager = BiometricManager.from(activity)
        val can = manager.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG)
        authOutcomeForCapability(can)?.let { return it }

        return suspendCancellableCoroutine { continuation ->
            val prompt = BiometricPrompt(
                activity,
                ContextCompat.getMainExecutor(activity),
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                        if (continuation.isActive) continuation.resume(AuthOutcome.SUCCEEDED)
                    }

                    override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                        if (continuation.isActive) continuation.resume(authOutcomeForError(errorCode))
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
            prompt.authenticate(info, BiometricPrompt.CryptoObject(cipher))
        }
    }
}
