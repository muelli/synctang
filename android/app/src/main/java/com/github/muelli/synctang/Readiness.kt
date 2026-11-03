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

/**
 * How often the Ready screen asks, while it is showing and the app is in the foreground:
 * every minute, give or take [READINESS_JITTER]. The machine announces itself every 60 s and
 * a record counts as fresh for 150 s, so asking more often cannot tell anything new, and
 * the public discovery server is shared: it answers a client that asks every few seconds
 * with HTTP 429 and a Retry-After of minutes. There is also one check at once when the
 * screen appears or the app comes back, and a person may ask by hand (the Check again
 * button, or a long press on the chip), which is held to [READINESS_MIN_GAP_MILLIS] and to
 * any Retry-After the server has given.
 */
internal const val READINESS_POLL_MILLIS = 60_000L

/** How far, as a fraction, one poll interval moves either way, so that phones do not ask together. */
internal const val READINESS_JITTER = 0.2

/**
 * Two checks are never started closer together than this, whatever asked for them (a tap, the
 * app coming back to the foreground): what was shown less than this long ago is not stale.
 */
internal const val READINESS_MIN_GAP_MILLIS = 10_000L

/** [base] moved by up to [READINESS_JITTER] either way: [random] in [0, 1] picks where. */
fun readinessPollDelayMillis(base: Long, random: Double): Long =
    (base * (1 + READINESS_JITTER * (2 * random.coerceIn(0.0, 1.0) - 1))).toLong()

/** The Go side's word for a discovery server that said to ask less often (HTTP 429). */
internal const val GO_RATE_LIMITED = "RATE_LIMITED"

/**
 * How long to leave the discovery server alone after it said to. The Go side already bounds
 * what it reports; this bounds it again where the wait is acted on: at least 30 s, at most
 * 10 minutes.
 */
fun rateLimitWaitMillis(retryAfterSeconds: Int): Long =
    (retryAfterSeconds.toLong() * 1000).coerceIn(30_000L, 600_000L)

/**
 * What a status check came back with: what the machine looks like, or "asked too often, wait
 * this long" ([RateLimited], which is not a state of the machine and changes nothing shown).
 */
sealed interface StatusAnswer {
    data class Known(val readiness: Readiness) : StatusAnswer
    data class RateLimited(val retryAfterMillis: Long) : StatusAnswer
}

/** An age as the words after "announced itself": seconds, minutes (under two hours), hours (under two days), days. */
fun ageLabel(seconds: Int): Label = when {
    seconds < 60 -> Label(R.string.age_seconds, listOf(max(seconds, 0)))
    seconds < 120 * 60 -> Label(R.string.age_minutes, listOf(seconds / 60))
    seconds < 48 * 3600 -> Label(R.string.age_hours, listOf(seconds / 3600))
    else -> Label(R.string.age_days, listOf(seconds / 86_400))
}

/** A headline for a failed dial: a string resource and, when it has one, the age to put in it. */
data class UnreachableHeadline(@StringRes val res: Int, val age: Label?)

/**
 * What a dial that gave up should say about the machine, from what discovery knows of it:
 * it announced itself lately and did not answer, it announced itself long ago, or never. Null
 * when discovery tells nothing (no network, still checking): the general headline stays.
 */
fun unreachableHeadline(r: Readiness): UnreachableHeadline? = when (r.state) {
    ReadinessState.WAITING -> UnreachableHeadline(R.string.failure_unreachable_waiting, ageLabel(max(r.ageSeconds, 0)))
    ReadinessState.STALE ->
        if (r.ageSeconds < 0) {
            UnreachableHeadline(R.string.failure_unreachable_stale_unknown, null)
        } else {
            UnreachableHeadline(R.string.failure_unreachable_stale, ageLabel(r.ageSeconds))
        }
    ReadinessState.ABSENT -> UnreachableHeadline(R.string.failure_unreachable_absent, null)
    ReadinessState.NO_NETWORK, ReadinessState.CHECKING -> null
}

/** How long a record counts as "waiting": the Go side's WaitingRecordMaxAge, 150 s. */
internal const val WAITING_MAX_AGE_SECONDS = 150

/**
 * A status seen [elapsedSeconds] ago, as it would read now: a record keeps ageing, and a
 * waiting one that passes 150 s is stale. Unchanged when it has no age to grow.
 */
fun agedSince(r: Readiness, elapsedSeconds: Int): Readiness {
    if ((r.state != ReadinessState.WAITING && r.state != ReadinessState.STALE) || r.ageSeconds < 0) return r
    val age = r.ageSeconds + max(elapsedSeconds, 0)
    return Readiness(if (age <= WAITING_MAX_AGE_SECONDS) ReadinessState.WAITING else ReadinessState.STALE, age)
}
