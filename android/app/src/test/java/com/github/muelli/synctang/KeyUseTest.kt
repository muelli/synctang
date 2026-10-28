// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * On a Pixel 3a XL (Android 12, Keymaster 4.x) using a key-agreement key
 * sometimes hung inside the Keystore for as long as anyone waited, logging
 * BACKEND_BUSY, while the app showed a spinner. A call stuck in the Keystore
 * cannot be interrupted, only abandoned, so the key use runs on its own
 * thread and the caller stops waiting after a limit.
 */
class KeyUseTest {

    @Test
    fun aKeyUseThatFinishesReturnsItsResult() {
        assertEquals(42, runWithin(5) { 42 })
    }

    @Test
    fun aFailureInsideTheKeyUseReachesTheCallerAsItself() {
        // The flow tells an invalidated key from an expired window by type,
        // so wrapping what the key throws would turn "start afresh" into
        // "something unexpected".
        val invalidated = KeyInvalidated(RuntimeException("Key permanently invalidated"))
        try {
            runWithin(5) { throw invalidated }
            fail("expected the failure")
        } catch (e: KeyInvalidated) {
            assertSame(invalidated, e)
        }
    }

    @Test
    fun aKeyUseThatNeverFinishesGivesUpAfterTheLimit() {
        val hung = CountDownLatch(1)
        val started = System.nanoTime()
        try {
            runWithin(1) { hung.await(); 0 }
            fail("expected a timeout")
        } catch (e: KeyUseTimedOut) {
            assertEquals(1L, e.seconds)
        } finally {
            hung.countDown()
        }
        val waited = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)
        assertTrue("waited ${waited}ms for a 1s limit", waited in 900..4000)
    }

    @Test
    fun theAbandonedKeyUseDoesNotKeepTheProcessAlive() {
        val hung = CountDownLatch(1)
        try {
            runWithin(1) { hung.await(); 0 }
        } catch (e: KeyUseTimedOut) {
            // expected
        }
        val abandoned = Thread.getAllStackTraces().keys.filter { it.name == KEY_USE_THREAD_NAME }
        hung.countDown()
        assertTrue("the thread running the key use must be a daemon", abandoned.all { it.isDaemon })
    }
}
