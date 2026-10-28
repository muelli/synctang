// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Every failure screen showed the Go error's own text, such as
 * "mobile: reading recovery request: EOF", which is written for a log
 * and tells the person holding the phone nothing. Each kind of failure
 * the Go bridge names now has words of its own. The machine's own
 * reason, where it gave one, is kept as a detail beneath them, and
 * the raw text of every failure stays one tap away, behind Details,
 * for a bug report.
 */
class ExplainTest {

    private fun failure(kind: FailureKind, detail: String = "") =
        MachineFailure(kind, detail, "mobile: raw text for $kind", null)

    @Test
    fun eachKindHasItsOwnMessage() {
        val expected = mapOf(
            FailureKind.UNREACHABLE to R.string.failure_unreachable,
            FailureKind.PAIR_UNREACHABLE to R.string.failure_pair_unreachable,
            FailureKind.HUNG_UP to R.string.failure_hung_up,
            FailureKind.NO_REPLY to R.string.failure_no_reply,
            FailureKind.OUTCOME_UNKNOWN to R.string.failure_outcome_unknown,
            FailureKind.ANSWER_REJECTED to R.string.failure_answer_rejected,
            FailureKind.BAD_PAIRING_CODE to R.string.failure_bad_pairing_code,
            FailureKind.PAIR_CODE_MISMATCH to R.string.failure_pair_code_mismatch,
            FailureKind.PAIR_DECLINED to R.string.failure_pair_declined,
            FailureKind.PAIR_REFUSED to R.string.failure_pair_refused,
            FailureKind.PROTOCOL to R.string.failure_protocol,
            FailureKind.OTHER to R.string.failure_other,
        )
        for ((kind, message) in expected) {
            assertEquals("message for $kind", message, explain(failure(kind)).message)
        }
        assertEquals(expected.size, expected.values.toSet().size)
    }

    @Test
    fun theMachinesOwnReasonIsKeptAsTheDetail() {
        val explained = explain(failure(FailureKind.ANSWER_REJECTED, "recovered secret does not open /dev/vda3"))
        assertEquals("recovered secret does not open /dev/vda3", explained.detail)
    }

    @Test
    fun aRecognisedFailureShowsNoDetail() {
        assertNull(explain(failure(FailureKind.UNREACHABLE)).detail)
        assertNull(explain(failure(FailureKind.PAIR_CODE_MISMATCH)).detail)
    }

    @Test
    fun anUnrecognisedFailureHasNoDetailEither() {
        assertNull(explain(failure(FailureKind.OTHER)).detail)
        assertNull(explain(failure(FailureKind.PROTOCOL)).detail)
        val plain = explain(IllegalStateException("something nobody expected"))
        assertEquals(R.string.failure_other, plain.message)
        assertNull(plain.detail)
    }

    @Test
    fun everyFailureKeepsItsRawTextForTheDetailsToggle() {
        for (kind in FailureKind.values()) {
            assertEquals("mobile: raw text for $kind", explain(failure(kind)).technical)
        }
        assertEquals("something nobody expected", explain(IllegalStateException("something nobody expected")).technical)
        assertEquals("IllegalStateException", explain(IllegalStateException()).technical)
    }

    @Test
    fun anExpiredBiometricWindowSaysToTryAgain() {
        assertEquals(R.string.failure_auth_expired, explain(AuthenticationExpired(RuntimeException())).message)
    }

    @Test
    fun aKeyThatDoesNotRespondHasItsOwnMessage() {
        assertEquals(R.string.failure_key_timed_out, explain(KeyUseTimedOut(20)).message)
    }
}
