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

    @Test
    fun theCodeIsFoundAnywhereInTheCauseChain() {
        val chain = javax.crypto.IllegalBlockSizeException("wrapped").apply { initCause(KeyStoreException(-26)) }
        assertTrue(chain.isUserNotAuthenticated())
    }

    @Test
    fun anotherCodeIsNotIt() {
        assertFalse(KeyStoreException(-1).isUserNotAuthenticated())
        assertFalse(KeyStoreException(0).isUserNotAuthenticated())
    }

    @Test
    fun aMessageThatSaysSoIsNotEnough() {
        assertFalse(RuntimeException("Key user not authenticated").isUserNotAuthenticated())
        assertFalse(java.security.KeyStoreException("Key user not authenticated").isUserNotAuthenticated())
    }

    @Test
    fun aChainWithoutOneIsNotIt() {
        assertFalse(IllegalStateException("x", RuntimeException("y")).isUserNotAuthenticated())
    }
}
