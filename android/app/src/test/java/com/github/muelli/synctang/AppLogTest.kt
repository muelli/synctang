// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The logging facade the flow writes through. A release build asks for warnings only,
 * and then the messages for the lower levels are not even built: they are lambdas, and
 * building one (a state described, a cause chain walked) must cost nothing when nobody
 * is reading.
 */
class AppLogTest {

    private class Capture : AppLog.Sink {
        val lines = mutableListOf<Pair<AppLog.Level, String>>()
        override fun write(level: AppLog.Level, tag: String, line: String, t: Throwable?) {
            assertEquals(LOG_TAG, tag)
            lines += level to line
        }
    }

    @Test
    fun theMessageIsNotBuiltWhenTheLevelIsOff() {
        val sink = Capture()
        val log = AppLog(sink) { AppLog.Level.WARN } // what a release build asks for
        var built = 0
        log.d { built++; "debug" }
        log.i { built++; "info" }
        assertEquals("a message was built although its level is off", 0, built)
        assertTrue(sink.lines.isEmpty())
        log.w({ built++; "warn" })
        assertEquals(1, built)
        assertEquals(listOf(AppLog.Level.WARN to "warn"), sink.lines)
    }

    @Test
    fun aDebugBuildWritesEveryLevel() {
        val sink = Capture()
        val log = AppLog(sink) { AppLog.Level.DEBUG }
        log.d { "d" }
        log.i { "i" }
        log.w({ "w" })
        assertEquals(listOf("d", "i", "w"), sink.lines.map { it.second })
    }

    @Test
    fun theLevelIsReadAtEachCallSoItCanBeChanged() {
        val sink = Capture()
        var level = AppLog.Level.WARN
        val log = AppLog(sink) { level }
        log.d { "before" }
        level = AppLog.Level.DEBUG
        log.d { "after" }
        assertEquals(listOf("after"), sink.lines.map { it.second })
    }

    @Test
    fun aFailureIsLoggedWithItsWholeCauseChain() {
        val sink = Capture()
        val log = AppLog(sink) { AppLog.Level.WARN }
        val root = java.io.IOException("reading recover response: EOF")
        val e = MachineFailure(FailureKind.HUNG_UP, "", "mobile: the machine hung up", root)
        log.w({ "failed" }, e)
        val line = sink.lines.single().second
        assertTrue(line, line.contains("failed"))
        assertTrue(line, line.contains("MachineFailure: mobile: the machine hung up"))
        assertTrue(line, line.contains("caused by java.io.IOException: reading recover response: EOF"))
        assertFalse(line.contains("\n\n"))
    }

    @Test
    fun kidsAreShortenedToEightCharacters() {
        assertEquals("abcdef01", kidPrefix("abcdef0123456789abcdef0123456789"))
        assertEquals("abc", kidPrefix("abc"))
    }
}
