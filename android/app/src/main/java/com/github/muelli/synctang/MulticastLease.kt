// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Context
import android.net.wifi.WifiManager
import android.util.Log

/**
 * Holds a Wi-Fi MulticastLock for the duration of a dial.
 *
 * Android's Wi-Fi driver filters out multicast packets not addressed
 * to the device before they ever reach an application, to save power.
 * A MulticastLock turns that filter off. The machine announces itself
 * on a multicast group (transport.LocalDiscovery), so without this
 * lock the phone's local-discovery leg hears nothing at all and only
 * the relay path can ever find a machine, which is exactly the
 * situation that having a local path is meant to fix.
 *
 * The lock is scoped to one dial rather than to the app's lifetime
 * because it is a power trade: while it is held the Wi-Fi chip wakes
 * for multicast traffic belonging to everything else on the network
 * too. Dials are short and rare, so the narrow scope costs nothing.
 *
 * Nothing here is required for the app to work. A device with no
 * Wi-Fi service, or one that refuses the lock, loses local discovery
 * and keeps the relay, so this degrades rather than breaks.
 */
object MulticastLease {

    /**
     * The part of Android's WifiManager.MulticastLock this uses.
     * Named separately so the lease's logic can be tested without an
     * Android framework, since the real class cannot be constructed
     * off-device.
     */
    interface Lock {
        fun acquire()
        fun release()
    }

    /**
     * Runs [body] with [lock] held, releasing it however [body] ends.
     * A null lock runs [body] unchanged.
     */
    fun <T> holding(lock: Lock?, body: () -> T): T {
        lock?.acquire()
        try {
            return body()
        } finally {
            lock?.release()
        }
    }

    /**
     * A lock from the system WifiManager, or null if this device will
     * not give one out. Callers treat null as "no local discovery",
     * never as an error.
     *
     * A fresh lock per dial, deliberately: a shared one would need
     * reference counting to survive overlapping dials, and getting
     * that wrong leaks the lock rather than failing visibly.
     */
    fun lockFor(context: Context, tag: String = "synctang-local-discovery"): Lock? {
        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
            ?: return null
        return try {
            val lock = wifi.createMulticastLock(tag)
            lock.setReferenceCounted(false)
            object : Lock {
                override fun acquire() = lock.acquire()
                override fun release() {
                    if (lock.isHeld) lock.release()
                }
            }
        } catch (e: SecurityException) {
            // CHANGE_WIFI_MULTICAST_STATE missing or withheld: the
            // relay still works, so this is worth a log line and
            // nothing more.
            Log.w("MulticastLease", "no multicast lock, local discovery disabled", e)
            null
        }
    }
}
