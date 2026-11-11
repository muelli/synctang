// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.os.ParcelFileDescriptor
import androidx.test.platform.app.InstrumentationRegistry

/**
 * The app's own log (logcat, tag `synctang`) as a test reads it: what the flow was told and what it did,
 * which is how a test sees that something reached the app on a phone where the app cannot get as far as
 * the Unlock screen (a key in software stops it at a refusal).
 */
object AppLogReader {
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()

    private fun shell(command: String): String =
        ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand(command))
            .bufferedReader().use { it.readText() }

    fun clear() {
        shell("logcat -c")
    }

    fun read(): String = shell("logcat -d -s synctang:D")

    fun waitFor(needle: String, seconds: Int = 15): Boolean {
        val end = System.currentTimeMillis() + seconds * 1_000L
        while (System.currentTimeMillis() < end) {
            if (read().contains(needle)) return true
            Thread.sleep(300)
        }
        return false
    }
}
