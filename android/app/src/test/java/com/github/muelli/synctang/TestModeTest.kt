// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

/**
 * The no-biometric test mode removes the authentication requirement
 * from a key that unlocks a disk, so the interesting property is not
 * that it works but that it cannot leak into a build that believes it
 * is gated.
 */
class TestModeTest {

    @Test
    fun theTestKeyIsADifferentKeyFromTheRealOne() {
        assertNotEquals(
            "an ungated key must not share an alias with the gated one",
            TestMode.aliasFor(noBiometric = true),
            TestMode.aliasFor(noBiometric = false),
        )
    }

    @Test
    fun theOrdinaryAliasIsUnchanged() {
        // Changing this would silently orphan the key on every phone
        // that already has one, and with it that phone's enrolment on
        // every machine it can unlock.
        assertEquals("synctang-key-holder", TestMode.aliasFor(noBiometric = false))
    }

    @Test
    fun theTestAliasSaysWhatItIs() {
        assertEquals("synctang-key-holder-no-biometric", TestMode.aliasFor(noBiometric = true))
    }
}
