// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/**
 * The app's own log, for following what it does from `adb logcat -s synctang GoLog`.
 *
 * Messages are lambdas, built only if their level is on: a release build asks for
 * warnings and the debug and info messages cost nothing. [minLevel] is read at each
 * call, so the level can change while the app runs.
 *
 * Nothing secret is logged through this, and the callers are held to that by tests:
 * not the pairing code or link, the passphrase, scalars, wrapped or encrypted blobs,
 * public keys in full, or the identity seed. Device IDs, machine names, URLs and
 * addresses are fine, and a key identifier appears only as its [kidPrefix].
 */
class AppLog(private val sink: Sink, private val minLevel: () -> Level) {

    enum class Level { DEBUG, INFO, WARN }

    /** Where lines go: logcat in the app, a list in a test. */
    fun interface Sink {
        fun write(level: Level, tag: String, line: String, t: Throwable?)
    }

    fun d(message: () -> String) = log(Level.DEBUG, message, null)
    fun i(message: () -> String) = log(Level.INFO, message, null)
    fun w(message: () -> String, t: Throwable? = null) = log(Level.WARN, message, t)

    private fun log(level: Level, message: () -> String, t: Throwable?) {
        if (level < minLevel()) return
        val line = if (t == null) message() else message() + " " + causeChain(t)
        sink.write(level, LOG_TAG, line, t)
    }

    companion object {
        /**
         * Writes to android.util.Log under the tag `synctang`: everything in a debug build,
         * warnings only in a release build.
         */
        val logcat = AppLog(
            { level, tag, line, t ->
                when (level) {
                    Level.DEBUG -> android.util.Log.d(tag, line)
                    Level.INFO -> android.util.Log.i(tag, line)
                    Level.WARN -> if (t != null) android.util.Log.w(tag, line, t) else android.util.Log.w(tag, line)
                }
            },
        ) { if (BuildConfig.DEBUG) Level.DEBUG else Level.WARN }

        /** Writes nothing, for a flow that was given no log. */
        val silent = AppLog({ _, _, _, _ -> }) { Level.WARN }
    }
}

/**
 * [t] and every cause under it, as "Class: message, caused by Class: message", so a
 * failure that arrives wrapped three deep says what it was at the bottom.
 */
internal fun causeChain(t: Throwable): String {
    val parts = mutableListOf<String>()
    var cur: Throwable? = t
    var depth = 0
    while (cur != null && depth < 8) {
        // Short name for the outermost, qualified below it, where the package says where a
        // wrapped failure came from.
        val name = if (depth == 0) cur.javaClass.simpleName else cur.javaClass.name
        parts += (if (depth == 0) "" else "caused by ") + name + ": " + (cur.message ?: "")
        val next = cur.cause
        cur = if (next === cur) null else next
        depth++
    }
    return parts.joinToString(", ")
}

/** The first eight characters of a key identifier: enough to tell two apart, not the identifier. */
fun kidPrefix(kidHex: String): String = kidHex.take(8)
