// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes
import kotlin.math.max
import kotlin.math.pow

/**
 * What discovery says about the paired machine, as the Ready screen shows it before the
 * person walks to the machine: the Go side's `CheckMachineStatus` states, plus [CHECKING]
 * for before the first answer. It says whether the machine announced itself recently, not
 * that an unlock will work.
 */
enum class ReadinessState {
    CHECKING,
    WAITING,
    STALE,
    ABSENT,
    NO_NETWORK;

    companion object {
        /** The state for a string from the Go side; one this app does not know is [CHECKING]. */
        fun fromGo(s: String): ReadinessState = when (s) {
            "WAITING" -> WAITING
            "STALE" -> STALE
            "ABSENT" -> ABSENT
            "NO_NETWORK" -> NO_NETWORK
            else -> CHECKING
        }
    }
}

/** A state and, for a record that exists, how long ago the machine announced (-1 if unknown). */
data class Readiness(val state: ReadinessState = ReadinessState.CHECKING, val ageSeconds: Int = -1)

/** A string resource with the arguments to format it with. */
data class Label(@StringRes val res: Int, val args: List<Any> = emptyList())

/** The words shown next to the dot. Colour is never the only signal: every state has these. */
fun readinessLabel(r: Readiness): Label = when (r.state) {
    ReadinessState.CHECKING -> Label(R.string.readiness_checking)
    ReadinessState.WAITING -> Label(R.string.readiness_waiting)
    ReadinessState.STALE -> when {
        r.ageSeconds < 0 -> Label(R.string.readiness_stale_unknown)
        r.ageSeconds < 120 * 60 -> Label(R.string.readiness_stale_minutes, listOf(max(1, r.ageSeconds / 60)))
        r.ageSeconds < 48 * 3600 -> Label(R.string.readiness_stale_hours, listOf(r.ageSeconds / 3600))
        else -> Label(R.string.readiness_stale_days, listOf(r.ageSeconds / 86_400))
    }
    ReadinessState.ABSENT -> Label(R.string.readiness_absent)
    ReadinessState.NO_NETWORK -> Label(R.string.readiness_no_network)
}

/**
 * What a screen reader is told when the state changes. The same as the words, except that
 * it carries no age, so that it does not change (and is not read out again) as the age
 * does.
 */
@StringRes
fun readinessAnnouncement(state: ReadinessState): Int = when (state) {
    ReadinessState.CHECKING -> R.string.readiness_checking
    ReadinessState.WAITING -> R.string.readiness_waiting
    ReadinessState.STALE -> R.string.readiness_stale_announce
    ReadinessState.ABSENT -> R.string.readiness_absent
    ReadinessState.NO_NETWORK -> R.string.readiness_no_network
}

/** Whether the live region speaks: when the state changed, not on every poll and not for the age alone. */
fun shouldAnnounceReadiness(previous: Readiness?, next: Readiness): Boolean = previous?.state != next.state

/** The dark theme's surface, which the dot is drawn on. */
const val DARK_SURFACE = 0xFF141218L

/** The dot's colour: green, amber, red, grey, and a lighter blue-grey while checking. */
fun readinessDotColour(state: ReadinessState): Long = when (state) {
    ReadinessState.WAITING -> 0xFF66BB6AL
    ReadinessState.STALE -> 0xFFFFB74DL
    ReadinessState.ABSENT -> 0xFFEF5350L
    ReadinessState.NO_NETWORK -> 0xFF9E9E9EL
    ReadinessState.CHECKING -> 0xFF90A4AEL
}

/** The WCAG contrast ratio of two ARGB colours (alpha ignored). A graphic needs 3. */
fun contrastRatio(a: Long, b: Long): Double {
    val la = luminance(a)
    val lb = luminance(b)
    return (max(la, lb) + 0.05) / (kotlin.math.min(la, lb) + 0.05)
}

private fun luminance(argb: Long): Double {
    fun channel(shift: Int): Double {
        val c = ((argb shr shift) and 0xFF) / 255.0
        return if (c <= 0.03928) c / 12.92 else ((c + 0.055) / 1.055).pow(2.4)
    }
    return 0.2126 * channel(16) + 0.7152 * channel(8) + 0.0722 * channel(0)
}

/** How often the Ready screen asks, while it is showing and the app is in the foreground. */
internal const val READINESS_POLL_MILLIS = 5_000L
