// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import androidx.annotation.StringRes

/**
 * What went wrong talking to a machine, as the Go bridge names it (see
 * the Failure constants in mobile/failure.go), plus the distinction
 * between reaching a machine to unlock it and reaching one to pair,
 * which ask the person to check different things.
 */
enum class FailureKind {
    OTHER,
    CANCELLED,
    UNREACHABLE,
    PAIR_UNREACHABLE,
    HUNG_UP,
    NO_REPLY,
    OUTCOME_UNKNOWN,
    ANSWER_REJECTED,
    BAD_PAIRING_CODE,
    PAIR_CODE_MISMATCH,
    PAIR_DECLINED,
    PAIR_REFUSED,
    PROTOCOL,
}

/**
 * A failure from the Go bridge with its kind attached. gomobile turns
 * a Go error into a plain Exception carrying only its text, so the
 * adapters in UnlockViewModel ask the Go object for the kind and
 * rethrow it as this.
 */
class MachineFailure(
    val kind: FailureKind,
    val detail: String,
    message: String?,
    cause: Throwable?,
) : Exception(message, cause)

/**
 * A failure in words for the person holding the phone: a message, and
 * underneath it a [detail] that is either the machine's own reason or,
 * for a failure nothing here recognises, the raw error text, which is
 * then the only clue there is.
 */
data class Explained(@StringRes val message: Int, val detail: String?)

fun explain(e: Throwable): Explained = when (e) {
    is MachineFailure -> when (e.kind) {
        FailureKind.UNREACHABLE -> Explained(R.string.failure_unreachable, null)
        FailureKind.PAIR_UNREACHABLE -> Explained(R.string.failure_pair_unreachable, null)
        FailureKind.HUNG_UP -> Explained(R.string.failure_hung_up, null)
        FailureKind.NO_REPLY -> Explained(R.string.failure_no_reply, null)
        FailureKind.OUTCOME_UNKNOWN -> Explained(R.string.failure_outcome_unknown, null)
        FailureKind.ANSWER_REJECTED -> Explained(R.string.failure_answer_rejected, e.detail.ifBlank { null })
        FailureKind.BAD_PAIRING_CODE -> Explained(R.string.failure_bad_pairing_code, null)
        FailureKind.PAIR_CODE_MISMATCH -> Explained(R.string.failure_pair_code_mismatch, null)
        FailureKind.PAIR_DECLINED -> Explained(R.string.failure_pair_declined, null)
        FailureKind.PAIR_REFUSED -> Explained(R.string.failure_pair_refused, e.detail.ifBlank { null })
        FailureKind.PROTOCOL -> Explained(R.string.failure_protocol, e.rawText())
        FailureKind.OTHER, FailureKind.CANCELLED -> Explained(R.string.failure_other, e.rawText())
    }
    is AuthenticationExpired -> Explained(R.string.failure_auth_expired, null)
    else -> Explained(R.string.failure_other, e.rawText())
}

private fun Throwable.rawText(): String =
    message?.takeIf { it.isNotBlank() } ?: this::class.java.simpleName
