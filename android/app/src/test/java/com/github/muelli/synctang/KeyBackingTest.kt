// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyProperties
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the app tells the person about where their key lives has to follow
 * what Android says it did, not what the app asked for.
 *
 * Seen on a real Pixel 3a XL (Android 12, Keymaster 4.x hardware): a
 * key-agreement key requested with StrongBox came back at security level 0,
 * software, and its attestation certificate said the same, while ordinary
 * signing keys on the same phone were created in the TEE and in StrongBox.
 * Android 12 emulates key agreement in software where the hardware does not
 * offer it. The app reported that key as living in the trusted execution
 * environment.
 */
class KeyBackingTest {

    @Test
    fun strongBoxIsReportedAsStrongBox() {
        assertEquals(
            KeyHolderKey.Backing.STRONGBOX,
            KeyHolderKey.classify(KeyProperties.SECURITY_LEVEL_STRONGBOX),
        )
    }

    @Test
    fun theTrustedExecutionEnvironmentIsReportedAsTheTee() {
        assertEquals(
            KeyHolderKey.Backing.TEE,
            KeyHolderKey.classify(KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT),
        )
    }

    @Test
    fun aSoftwareKeyIsReportedAsSoftwareNotAsTheTee() {
        assertEquals(
            KeyHolderKey.Backing.SOFTWARE,
            KeyHolderKey.classify(KeyProperties.SECURITY_LEVEL_SOFTWARE),
        )
    }

    // "Secure, but not which" is still secure hardware; it must not be shown
    // as software.
    @Test
    fun anUnspecifiedSecureLevelIsNotCalledSoftware() {
        assertEquals(
            KeyHolderKey.Backing.TEE,
            KeyHolderKey.classify(KeyProperties.SECURITY_LEVEL_UNKNOWN_SECURE),
        )
    }

    // Not knowing is reported as not being able to confirm hardware, never as
    // hardware: the wording for SOFTWARE says so.
    @Test
    fun anUnknownLevelIsNeverReportedAsHardware() {
        assertEquals(
            KeyHolderKey.Backing.SOFTWARE,
            KeyHolderKey.classify(KeyProperties.SECURITY_LEVEL_UNKNOWN),
        )
        assertEquals(KeyHolderKey.Backing.SOFTWARE, KeyHolderKey.classify(99))
    }

    // What the app does about a software key (decided 2026-10-28: refuse
    // unless the person has allowed it on this phone).

    @Test
    fun aSoftwareKeyIsRefusedUnlessTheOwnerAllowedIt() {
        assertTrue(KeyHolderKey.refuses(KeyHolderKey.Backing.SOFTWARE, softwareAllowed = false))
        assertFalse(KeyHolderKey.refuses(KeyHolderKey.Backing.SOFTWARE, softwareAllowed = true))
    }

    @Test
    fun hardwareBackedKeysAreNeverRefused() {
        for (backing in listOf(KeyHolderKey.Backing.STRONGBOX, KeyHolderKey.Backing.TEE)) {
            assertFalse(KeyHolderKey.refuses(backing, softwareAllowed = false))
            assertFalse(KeyHolderKey.refuses(backing, softwareAllowed = true))
        }
    }
}
