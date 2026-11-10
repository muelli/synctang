// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes

/**
 * How far a dial, a pairing or an answer has got, as the Go side reports it
 * (`Session.stage`, `Pairer.stage`; mobile/progress.go). The numbers are the
 * contract with it. The busy screens show it as one line of words next to the
 * spinner, so that a long wait says what it is waiting for.
 */
enum class Stage(val goNumber: Long) {
    /** Nothing has been asked yet. */
    NONE(0),

    /** The call has begun; nothing has happened on the network. */
    STARTING(1),

    /** Finding out where the machine is. */
    LOOKING_UP(2),

    /** Reaching the machine's address. */
    CONNECTING(3),

    /** Connected; checking that it is the machine that was meant. */
    CHECKING_IDENTITY(4),

    /** Identity confirmed; waiting for the machine to ask for an answer. */
    WAITING_FOR_REQUEST(5),

    /** The machine's request is here. Seen on the busy screen only while the answer is prepared. */
    REQUEST_READY(6),

    /** The answer is being sent. */
    SENDING_ANSWER(7),

    /** The answer is sent; the machine is checking that it opens the volume. */
    WAITING_FOR_VERDICT(8),

    /** The machine accepted the answer. */
    UNLOCKED(9),

    /** (Pairing) the pairing code's proof is being sent. */
    SENDING_PROOF(10),

    /** (Pairing) a person at the machine is being asked to confirm. The long wait. */
    WAITING_FOR_OPERATOR(11),

    /** (Pairing) the machine enrolled this phone. */
    ENROLLED(12);

    companion object {
        /** The stage for a number from the Go side; a number this app does not know is [NONE]. */
        fun fromGo(n: Long): Stage = values().firstOrNull { it.goNumber == n } ?: NONE
    }
}

/** Which way the machine was reached, once that is known. */
enum class Route(val goNumber: Long) {
    UNKNOWN(0),
    RELAY(1),
    LOCAL(2),
    DIRECT(3);

    companion object {
        fun fromGo(n: Long): Route = values().firstOrNull { it.goNumber == n } ?: UNKNOWN
    }
}

/**
 * What a busy screen shows. [attempt] counts dials (more than one means the first
 * did not get through). [startedAtMillis] is when the screen first appeared, so
 * that the time spent is counted from then however often the rest is updated.
 */
data class Progress(
    val stage: Stage = Stage.NONE,
    val attempt: Int = 0,
    val route: Route = Route.UNKNOWN,
    val startedAtMillis: Long = 0,
    /**
     * (Pairing) The six digits, such as "483 291", that the machine shows its operator with the question
     * whether to enrol this phone; empty until the request has been sent. The phone shows them too, for the
     * person to compare: the pairing code only proves that whoever answered had it.
     */
    val shortCode: String = "",
)

/**
 * The words for where [progress] has got, as a string resource: short enough to be
 * one line under the headline. Only where the way the machine was reached matters
 * to a person (a relay on the Internet, or the local network) does the route
 * change them.
 */
@StringRes
fun stageDetail(progress: Progress): Int = when (progress.stage) {
    Stage.NONE -> R.string.stage_none
    Stage.STARTING -> R.string.stage_starting
    Stage.LOOKING_UP -> R.string.stage_looking_up
    Stage.CONNECTING -> R.string.stage_connecting
    Stage.CHECKING_IDENTITY -> R.string.stage_checking_identity
    Stage.WAITING_FOR_REQUEST -> when (progress.route) {
        Route.RELAY -> R.string.stage_waiting_for_request_relay
        Route.LOCAL -> R.string.stage_waiting_for_request_local
        else -> R.string.stage_waiting_for_request
    }
    Stage.REQUEST_READY -> R.string.stage_request_ready
    Stage.SENDING_ANSWER -> R.string.stage_sending_answer
    Stage.WAITING_FOR_VERDICT -> R.string.stage_waiting_for_verdict
    Stage.UNLOCKED -> R.string.stage_unlocked
    Stage.SENDING_PROOF -> R.string.stage_sending_proof
    Stage.WAITING_FOR_OPERATOR -> R.string.stage_waiting_for_operator
    Stage.ENROLLED -> R.string.stage_enrolled
}
