// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/**
 * What the Approve screen says about the time left to answer: [seconds] rounded up
 * to a multiple of five, and whether it is [runningOut], which is when a polite
 * announcement is made, once.
 */
data class TimeLeft(val seconds: Int, val runningOut: Boolean)

/** From this much time left, the screen warns once that it is running out. */
internal const val RUNNING_OUT_MILLIS = 15_000L

/** The time left is shown in steps of this much, so it changes about every five seconds. */
internal const val TIME_LEFT_STEP_MILLIS = 5_000L

/**
 * The time left until [expiresAtMillis] as of [nowMillis], never below zero.
 * Rounded up, so that "5 s" is still shown while there is any time left and "0 s"
 * only when there is none.
 */
fun approveTimeLeft(expiresAtMillis: Long, now: Long): TimeLeft {
    val left = (expiresAtMillis - now).coerceAtLeast(0)
    val steps = (left + TIME_LEFT_STEP_MILLIS - 1) / TIME_LEFT_STEP_MILLIS
    return TimeLeft((steps * TIME_LEFT_STEP_MILLIS / 1_000).toInt(), left <= RUNNING_OUT_MILLIS)
}
