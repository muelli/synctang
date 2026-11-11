// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.SizeF
import android.widget.RemoteViews

/** What the widget shows: the machine it unlocks, or that there is none yet. */
sealed interface WidgetContent {
    data class ForMachine(val deviceId: String, val label: String) : WidgetContent
    object NotPaired : WidgetContent
}

/**
 * The machine a widget unlocks: the one the phone is paired with (the one used last, when there are more),
 * named as it may be shown outside the app ([launcherLabel]). Nothing says which machine a widget was placed
 * for yet, so every widget follows the current one.
 */
fun widgetContent(machines: List<Machine>): WidgetContent =
    machines.firstOrNull()?.let { WidgetContent.ForMachine(it.deviceId, launcherLabel(it)) } ?: WidgetContent.NotPaired

/**
 * The home screen widget: tap it and the phone unlocks the machine, as the Unlock button does (the dial, then
 * the fingerprint prompt naming the machine). It can be resized; the system picks, from the layouts offered, the
 * one that fits the size it has been given (one cell, wide, large).
 *
 * It shows nothing that is looked up: no readiness, so it never asks the network on its own and cannot show a
 * state that has gone stale. Its tap goes to [UnlockEntryActivity], which is not exported, through a
 * `PendingIntent` that is immutable and names the component.
 */
class UnlockWidgetProvider : AppWidgetProvider() {

    override fun onUpdate(context: Context, manager: AppWidgetManager, appWidgetIds: IntArray) {
        val content = widgetContent(PairingStore(context).machines())
        for (id in appWidgetIds) manager.updateAppWidget(id, views(context, content, id))
    }

    companion object {
        /** The smallest size, in dp, each layout is meant for; the system picks the one that fits best. */
        private val SMALL = SizeF(40f, 40f)
        private val WIDE = SizeF(110f, 40f)
        private val LARGE = SizeF(110f, 110f)

        /** Redraws every widget that is placed, after the machines changed. */
        fun refreshAll(context: Context, machines: List<Machine>) {
            val manager = AppWidgetManager.getInstance(context)
            val ids = manager.getAppWidgetIds(ComponentName(context, UnlockWidgetProvider::class.java))
            if (ids.isEmpty()) return
            val content = widgetContent(machines)
            for (id in ids) manager.updateAppWidget(id, views(context, content, id))
        }

        /** The three layouts, each with its text and its tap, keyed by the size it is for. */
        internal fun layouts(context: Context, content: WidgetContent, widgetId: Int): Map<SizeF, RemoteViews> {
            val (name, description, tap) = when (content) {
                is WidgetContent.ForMachine -> Triple(
                    content.label,
                    context.getString(R.string.widget_unlock_for, content.label),
                    unlockIntent(context, content.deviceId, widgetId),
                )
                WidgetContent.NotPaired -> Triple(
                    context.getString(R.string.widget_unpaired_name),
                    context.getString(R.string.widget_unpaired_for),
                    openAppIntent(context, widgetId),
                )
            }
            fun build(layout: Int, sized: Boolean) = RemoteViews(context.packageName, layout).apply {
                if (sized) setTextViewText(R.id.widget_name, name)
                setContentDescription(android.R.id.background, description)
                setOnClickPendingIntent(android.R.id.background, tap)
            }
            return mapOf(
                SMALL to build(R.layout.widget_unlock_small, sized = false),
                WIDE to build(R.layout.widget_unlock_wide, sized = true),
                LARGE to build(R.layout.widget_unlock_large, sized = true),
            )
        }

        private fun views(context: Context, content: WidgetContent, widgetId: Int) =
            RemoteViews(layouts(context, content, widgetId))

        // A different data URI for each widget, so that their PendingIntents are different ones: the
        // extras do not count when the system compares intents.
        private fun unlockIntent(context: Context, deviceId: String, widgetId: Int): PendingIntent =
            PendingIntent.getActivity(
                context,
                widgetId,
                UnlockEntryActivity.intentFor(context, deviceId).setData(Uri.parse("synctang-widget://$widgetId")),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )

        private fun openAppIntent(context: Context, widgetId: Int): PendingIntent =
            PendingIntent.getActivity(
                context,
                widgetId,
                Intent(context, MainActivity::class.java).setData(Uri.parse("synctang-widget://$widgetId")),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
    }
}
