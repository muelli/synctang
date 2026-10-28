// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.util.concurrent.ExecutionException
import java.util.concurrent.FutureTask
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

/**
 * How long the app waits for the Keystore to do the one ECDH. It takes tens
 * of milliseconds when it works, so this is only ever reached when the
 * Keystore is stuck (BACKEND_BUSY on a Pixel 3a XL, intermittently, was
 * seen to hang for as long as anyone waited).
 */
const val KEY_USE_TIMEOUT_SECONDS = 20L

internal const val KEY_USE_THREAD_NAME = "synctang-key-use"

/**
 * The Keystore did not answer within [seconds]. The call that is stuck
 * cannot be interrupted and is still running somewhere; the app has only
 * stopped waiting for it.
 */
class KeyUseTimedOut(val seconds: Long) :
    Exception("the Keystore did not answer within $seconds seconds")

/**
 * Runs [block], which uses the Keystore, and gives up waiting after
 * [seconds].
 *
 * A call stuck inside the Keystore is a binder call that Java cannot
 * interrupt, so [block] runs on a thread of its own and a stuck one is
 * abandoned, not stopped. The thread is a daemon so it cannot keep the
 * process alive. Whatever [block] throws reaches the caller as itself,
 * because the flow tells an invalidated key from an expired window by type.
 */
internal fun <T> runWithin(seconds: Long, block: () -> T): T {
    val task = FutureTask(block)
    Thread(task, KEY_USE_THREAD_NAME).apply { isDaemon = true }.start()
    try {
        return task.get(seconds, TimeUnit.SECONDS)
    } catch (e: TimeoutException) {
        throw KeyUseTimedOut(seconds)
    } catch (e: ExecutionException) {
        throw e.cause ?: e
    } catch (e: InterruptedException) {
        Thread.currentThread().interrupt()
        throw e
    }
}
