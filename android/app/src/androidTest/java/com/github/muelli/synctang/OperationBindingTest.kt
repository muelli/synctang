// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.util.Log
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.launch
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * A fingerprint given for one operation cannot be spent on another, and cannot be
 * spent twice. This is the claim the threat model marks as inferred, and it can only
 * be shown with a finger on the sensor, so it runs only when asked for:
 *
 *     adb shell am instrument -w -e fingerprint true \
 *       -e class com.github.muelli.synctang.OperationBindingTest \
 *       com.github.muelli.synctang.debug.test/androidx.test.runner.AndroidJUnitRunner
 *
 * then touch the sensor once when the prompt appears (it waits 3 minutes). The phone has to be awake and
 * unlocked, with a fingerprint enrolled.
 */
class OperationBindingTest {

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val directory = File(context.cacheDir, "operation-binding-test")
    private val key = KeyHolderKey(directory, "synctang-operation-binding", requireUserAuthentication = true)

    @After
    fun tearDown() {
        key.delete()
        directory.deleteRecursively()
    }

    private fun refusedForWantOfAuthentication(block: () -> Unit) {
        try {
            block()
            fail("the operation went through without its own authentication")
        } catch (e: KeyRefusedFingerprint) {
            assertTrue("refused, but not with 'user not authenticated': $e", e.cause!!.isUserNotAuthenticated())
        }
    }

    @Test
    fun anAuthenticationServesTheOperationItWasGivenForAndNoOther() {
        assumeTrue(
            "needs a person at the sensor: pass -e fingerprint true",
            InstrumentationRegistry.getArguments().getString("fingerprint") == "true",
        )
        directory.deleteRecursively()
        try {
            key.ensureKey()
        } catch (e: BiometricEnrolmentRequired) {
            assumeTrue("no biometric is enrolled on this device", false)
        }
        val wrapped = File(directory, "synctang-operation-binding.wrapped").readBytes()
        val cipherA = key.prepareUnwrap()
        val cipherB = key.prepareUnwrap()

        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            val outcome = AtomicReference<AuthOutcome?>(null)
            scenario.onActivity { activity ->
                val gate = BiometricGateImpl(activity)
                // Started on the main thread, which the prompt requires, and not waited
                // for here: the wait is below, on the test's own thread.
                MainScope().launch {
                    outcome.set(gate.authenticate("Operation binding test", "Touch the sensor once", cipherA))
                }
            }
            val end = System.currentTimeMillis() + 180_000
            while (outcome.get() == null && System.currentTimeMillis() < end) Thread.sleep(200)
            assertEquals("the fingerprint was not accepted: ${outcome.get()}", AuthOutcome.SUCCEEDED, outcome.get())
        }

        // The other operation, begun before, gets nothing from A's fingerprint.
        refusedForWantOfAuthentication { key.unwrap(cipherB).fill(0) }

        // The one that was authenticated works, once.
        val scalar = key.unwrap(cipherA)
        assertEquals(32, scalar.size)
        scalar.fill(0)
        Log.i("OperationBindingTest", "operation A unwrapped with its own authentication; B was refused")

        // And not twice.
        refusedForWantOfAuthentication { key.unwrap(cipherA).fill(0) }
        assertTrue(wrapped.isNotEmpty())
    }
}
