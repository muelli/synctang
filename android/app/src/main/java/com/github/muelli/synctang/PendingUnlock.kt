// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/**
 * How long an unlock asked for from outside the app is kept for the app to pick up: the time it takes the
 * app to come to the front and be ready, and not longer, so that one asked for long ago cannot start later.
 */
internal const val PENDING_UNLOCK_MILLIS = 10_000L

/**
 * The machine a shortcut or a widget asked to unlock, handed from [UnlockEntryActivity] to the app.
 *
 * It lives in the process and nowhere else, and only that activity sets it. [MainActivity] is exported, so
 * anything on the phone can start it with any extras; it therefore reads nothing from its own intent, and
 * takes the request from here. Held once, for [PENDING_UNLOCK_MILLIS], and only if it looks like a Device ID.
 */
class PendingUnlock(private val clock: () -> Long = System::currentTimeMillis) {
    private var deviceId: String? = null
    private var at = 0L

    @Synchronized
    fun set(candidate: String) {
        deviceId = candidate.takeIf { DEVICE_ID.matches(it) }
        at = clock()
    }

    @Synchronized
    fun take(): String? {
        val id = deviceId
        deviceId = null
        return id?.takeIf { clock() - at <= PENDING_UNLOCK_MILLIS }
    }

    companion object {
        /** The one in the app, shared by the entry activity and the main one. */
        val app = PendingUnlock()

        /** The canonical Device ID: eight groups of seven base32 characters. */
        private val DEVICE_ID = Regex("[A-Z2-7]{7}(-[A-Z2-7]{7}){7}")
    }
}
