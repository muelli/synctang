// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle

/**
 * The way in for a shortcut on the launcher and for the widget: "unlock this machine".
 *
 * It is not exported, so only this app's own shortcuts and `PendingIntent`s can start it; nothing else on the
 * phone can. It has no screen. It hands the Device ID it was given to [PendingUnlock] and starts the app, which
 * reads nothing from the intent that starts it (it is exported, for the launcher) and takes the request from
 * there. What happens next is the Unlock button's one tap: the app dials, and the fingerprint is what lets the
 * answer go.
 */
class UnlockEntryActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        intent?.getStringExtra(EXTRA_MACHINE)?.let { PendingUnlock.app.set(it) }
        startActivity(
            Intent(this, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
        )
        finish()
    }

    companion object {
        const val ACTION_UNLOCK = "com.github.muelli.synctang.action.UNLOCK"
        const val EXTRA_MACHINE = "com.github.muelli.synctang.extra.MACHINE"

        /** The intent a shortcut or a widget carries to unlock the machine with this Device ID. */
        fun intentFor(context: Context, deviceId: String): Intent =
            Intent(context, UnlockEntryActivity::class.java)
                .setAction(ACTION_UNLOCK)
                .putExtra(EXTRA_MACHINE, deviceId)
    }
}
