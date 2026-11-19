// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * When the phone can use a key agreement key that needs a fingerprint for each use: BiometricPrompt.CryptoObject
 * takes a KeyAgreement from Android 16 QPR2 (API 36.1) on. The constructor is what says so, not the number
 * 36, which also names the Android before it; it is looked for, never assumed.
 */
class HardwareAgreementTest {
    @Test
    fun anAndroidBefore36NeverHasIt() {
        assertFalse(HardwareAgreement.supportedWith(35) { true })
        assertFalse(HardwareAgreement.supportedWith(31) { true })
    }

    @Test
    fun anAndroid36WithoutTheConstructorDoesNotHaveIt() {
        assertFalse(HardwareAgreement.supportedWith(36) { false })
    }

    @Test
    fun anAndroid36OrNewerWithTheConstructorHasIt() {
        assertTrue(HardwareAgreement.supportedWith(36) { true })
        assertTrue(HardwareAgreement.supportedWith(37) { true })
    }

    @Test
    fun theConstructorIsNotLookedForOnAnAndroidThatCannotHaveIt() {
        var asked = false
        HardwareAgreement.supportedWith(34) { asked = true; true }
        assertFalse("looked for the constructor on API 34", asked)
    }

    @Test
    fun aLookupThatFailsIsNoSupport() {
        assertFalse(HardwareAgreement.supportedWith(36) { throw LinkageError("no such class") })
    }
}
