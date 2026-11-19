// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.hardware.biometrics.BiometricPrompt as FrameworkPrompt
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import android.os.CancellationSignal
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
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
class BiometricGateImpl(private val activity: FragmentActivity) : BiometricGate, AgreementGate {

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

    /**
     * The same for a key agreement key in the secure hardware: the prompt is the framework's own, because the androidx
     * prompt cannot be given a KeyAgreement (`BiometricPrompt.CryptoObject(KeyAgreement)` is new in Android 16 QPR2). Shown
     * from the activity in the foreground, as the system requires; what is authenticated is exactly this agreement, and
     * only after it succeeds can it be finished with the machine's point.
     */
    override suspend fun authenticate(title: String, subtitle: String, agreement: KeyAgreement): AuthOutcome {
        val manager = BiometricManager.from(activity)
        val can = manager.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG)
        authOutcomeForCapability(can)?.let { return it }

        return suspendCancellableCoroutine { continuation ->
            val executor = ContextCompat.getMainExecutor(activity)
            val signal = CancellationSignal()
            val prompt = FrameworkPrompt.Builder(activity)
                .setTitle(title)
                .setSubtitle(subtitle)
                .setNegativeButton(activity.getString(R.string.biometric_cancel), executor) { _, _ ->
                    if (continuation.isActive) continuation.resume(AuthOutcome.CANCELLED)
                }
                .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
                .build()
            continuation.invokeOnCancellation { signal.cancel() }
            prompt.authenticate(
                HardwareAgreement.cryptoObject(agreement),
                signal,
                executor,
                object : FrameworkPrompt.AuthenticationCallback() {
                    override fun onAuthenticationSucceeded(result: FrameworkPrompt.AuthenticationResult) {
                        if (continuation.isActive) continuation.resume(AuthOutcome.SUCCEEDED)
                    }

                    override fun onAuthenticationError(errorCode: Int, errString: CharSequence?) {
                        if (continuation.isActive) continuation.resume(authOutcomeForError(errorCode))
                    }

                    // A single non-matching finger is not a decision: the prompt stays up.
                    override fun onAuthenticationFailed() = Unit
                },
            )
        }
    }
}
