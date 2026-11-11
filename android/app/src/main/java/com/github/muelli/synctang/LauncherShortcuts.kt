// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.Context
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat

/**
 * The shortcuts that a long press on the app's icon shows: "Unlock <machine>", one for each paired machine, as
 * many as the launcher shows. Each leads to [UnlockEntryActivity], which is not exported.
 *
 * What is published replaces what was: a forgotten machine's shortcut goes, and a machine's new name comes.
 * Names are cleaned and cut on the way ([shortcutSpecs]); they are the machine's own words, and on a launcher
 * they are read by whoever holds the phone.
 */
object LauncherShortcuts {
    fun publish(context: Context, machines: List<Machine>) {
        try {
            val limit = ShortcutManagerCompat.getMaxShortcutCountPerActivity(context)
            val infos = shortcutSpecs(machines, limit).mapIndexed { rank, spec ->
                val label = context.getString(R.string.shortcut_unlock, spec.label)
                ShortcutInfoCompat.Builder(context, spec.id)
                    .setShortLabel(label)
                    .setLongLabel(label)
                    .setIcon(IconCompat.createWithResource(context, R.mipmap.ic_launcher))
                    .setIntent(UnlockEntryActivity.intentFor(context, spec.deviceId))
                    .setRank(rank)
                    .build()
            }
            ShortcutManagerCompat.setDynamicShortcuts(context, infos)
        } catch (e: Exception) {
            // The system rate-limits a background app and may refuse for other reasons; the shortcuts are a
            // convenience, and the app works without them.
            AppLog.logcat.w({ "could not publish the launcher shortcuts" }, e)
        }
    }
}
