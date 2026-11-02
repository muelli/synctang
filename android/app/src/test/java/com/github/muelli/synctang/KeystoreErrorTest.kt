// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import com.github.muelli.synctang.fake.KeyStoreException
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * After a successful prompt, the Keystore refusing the operation is told apart
 * by its numeric code (Keymaster's -26, "key user not authenticated"), never by its
 * message, which differs between Android versions and languages.
 */
class KeystoreErrorTest {

    private companion object {
        // The test double's own full name, standing for android.security.KeyStoreException.
        val FAKE = KeyStoreException::class.java.name
    }

    @Test
    fun theCodeIsFoundAnywhereInTheCauseChain() {
        val chain = javax.crypto.IllegalBlockSizeException("wrapped").apply { initCause(KeyStoreException(-26)) }
        assertTrue(chain.isUserNotAuthenticated(FAKE))
    }

    @Test
    fun anotherCodeIsNotIt() {
        assertFalse(KeyStoreException(-1).isUserNotAuthenticated(FAKE))
        assertFalse(KeyStoreException(0).isUserNotAuthenticated(FAKE))
    }

    @Test
    fun aMessageThatSaysSoIsNotEnough() {
        assertFalse(RuntimeException("Key user not authenticated").isUserNotAuthenticated(FAKE))
        assertFalse(java.security.KeyStoreException("Key user not authenticated").isUserNotAuthenticated(FAKE))
    }

    @Test
    fun aChainWithoutOneIsNotIt() {
        assertFalse(IllegalStateException("x", RuntimeException("y")).isUserNotAuthenticated(FAKE))
    }

    /** Only the platform's own class counts: a lookalike with the same simple name and accessor does not. */
    @Test
    fun aLookalikeIsNotThePlatformsClass() {
        assertFalse(KeyStoreException(-26).isUserNotAuthenticated())
    }

    /** A cause cycle is legal to construct, and a loop that never ends would hang the screen it is asked on. */
    @Test
    fun aCauseCycleEndsTheSearch() {
        val a = RuntimeException("a")
        val b = RuntimeException("b", a)
        a.initCause(b)
        assertFalse(a.isUserNotAuthenticated(FAKE))
    }
}
