// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.Build
import javax.crypto.KeyAgreement

/**
 * A key agreement key in the secure hardware that needs a fingerprint for each use, bound to that one operation:
 * the phone's key does the multiplication and hands back the x-coordinate and nothing else, so there is no scalar
 * for the app to hold, which the wrapped key cannot offer. It needs
 * `BiometricPrompt.CryptoObject(KeyAgreement)`, new in Android 16 QPR2 (API 36.1). It is looked for on the device,
 * never assumed from a version number, and an app that does not find it keeps the wrapped key ([KeyHolderKey]).
 *
 * `[V]` on the 36.1 emulator image in CI (`KeyAgreementProbeTest`, then `AgreementKeyTest`): the constructor exists, an
 * agreement that is not authenticated is refused, the prompt succeeds for the CryptoObject, and the secret
 * that follows is the software ECDH's. `[U]` on a real phone with a real sensor and secure hardware.
 */
object HardwareAgreement {
    private const val FIRST_API = 36
    private const val CRYPTO_OBJECT = "android.hardware.biometrics.BiometricPrompt\$CryptoObject"

    /** The decision, with what is known about the device passed in so that it can be tested. */
    fun supportedWith(sdkInt: Int, constructorPresent: () -> Boolean): Boolean {
        if (sdkInt < FIRST_API) return false
        return try {
            constructorPresent()
        } catch (e: LinkageError) {
            false
        } catch (e: ReflectiveOperationException) {
            false
        }
    }

    /** Whether this device can use it. The test build without a biometric gate does not: it has no fingerprint. */
    fun supported(): Boolean = !TestMode.noBiometric && supportedWith(Build.VERSION.SDK_INT) { constructor() != null }

    private fun constructor() = Class.forName(CRYPTO_OBJECT).getConstructor(KeyAgreement::class.java)

    /** A framework CryptoObject for [agreement], to hand to the framework's BiometricPrompt. */
    fun cryptoObject(agreement: KeyAgreement): android.hardware.biometrics.BiometricPrompt.CryptoObject =
        constructor().newInstance(agreement) as android.hardware.biometrics.BiometricPrompt.CryptoObject
}
