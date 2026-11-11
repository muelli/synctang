// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/** One shortcut on the launcher: [id] is its identity there, [label] the machine's name as it may be shown. */
data class ShortcutSpec(val id: String, val deviceId: String, val label: String)

/** The longest a machine's name is when it appears outside the app, in characters. */
internal const val SHORTCUT_LABEL_MAX = 24

/**
 * The shortcuts to offer for [machines], in the order given, at most [limit] and one for each Device ID; none
 * for a machine that needs pairing again, which cannot be unlocked.
 * The order is the caller's to choose (the machine used last first), because a launcher shows the first few.
 */
fun shortcutSpecs(machines: List<Machine>, limit: Int): List<ShortcutSpec> =
    machines.filter { !it.needsPairing }.distinctBy { it.deviceId }.take(limit.coerceAtLeast(0)).map {
        ShortcutSpec("unlock-${it.deviceId}", it.deviceId, launcherLabel(it))
    }

/**
 * The machine's name made fit for a launcher: it is what the machine called itself, which nothing proves and
 * anybody who can run a machine may set, so control characters and the invisible marks that reorder text are
 * dropped, runs of white space become one space, and it is cut to [SHORTCUT_LABEL_MAX] characters with an
 * ellipsis. With nothing left, the first group of the Device ID stands in.
 */
internal fun launcherLabel(machine: Machine): String {
    val cleaned = machine.name
        .filter { !it.isISOControl() && Character.getType(it) != Character.FORMAT.toInt() }
        .trim()
        .replace(Regex("\\s+"), " ")
    if (cleaned.isEmpty()) return machine.deviceId.substringBefore('-')
    val points = cleaned.codePointCount(0, cleaned.length)
    if (points <= SHORTCUT_LABEL_MAX) return cleaned
    val end = cleaned.offsetByCodePoints(0, SHORTCUT_LABEL_MAX - 1)
    return cleaned.substring(0, end) + "…"
}
