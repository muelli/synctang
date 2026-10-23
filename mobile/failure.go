// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"errors"
	"sync"
)

// What went wrong, as one of a fixed set of kinds, so that the app can
// explain a failure in its own words instead of showing a Go error
// string written for a log. Session.LastFailure and Pairer.LastFailure
// report them; the numbers are part of the contract with the app and
// must not be reused for something else.
const (
	// FailureNone: the last call succeeded.
	FailureNone = 0
	// FailureOther: nothing more specific is known; the error's own
	// text is all there is.
	FailureOther = 1
	// FailureCancelled: Cancel (or Close) interrupted the call.
	FailureCancelled = 2
	// FailureUnreachable: no attempt got as far as the machine's first
	// message before the timeout. The machine may be off, not at its
	// unlock prompt, out of reach, or not have this phone enrolled:
	// from this side those look the same.
	FailureUnreachable = 3
	// FailureHungUp: the machine closed the connection mid-exchange,
	// which after a confirm code usually means the code was wrong.
	FailureHungUp = 4
	// FailureNoReply: the machine stopped answering mid-exchange.
	FailureNoReply = 5
	// FailureOutcomeUnknown: the answer was sent, but the machine's
	// verdict never arrived, so it may or may not have unlocked.
	FailureOutcomeUnknown = 6
	// FailureAnswerRejected: the machine reported that it could not
	// use the answer. LastFailureDetail is the machine's reason.
	FailureAnswerRejected = 7
	// FailureBadPairingCode: the pairing code is malformed; nothing
	// was dialled.
	FailureBadPairingCode = 8
	// FailurePairCodeMismatch: the machine refused the pairing because
	// the code did not match.
	FailurePairCodeMismatch = 9
	// FailurePairDeclined: the machine's operator said no.
	FailurePairDeclined = 10
	// FailurePairRefused: the machine refused for another reason,
	// given by LastFailureDetail.
	FailurePairRefused = 11
	// FailureProtocol: the machine sent something this phone will not
	// act on.
	FailureProtocol = 12
)

// kindError attaches a failure kind, and optionally a detail for the
// person, to an error without changing its text.
type kindError struct {
	kind   int
	detail string
	err    error
}

func (e *kindError) Error() string { return e.err.Error() }
func (e *kindError) Unwrap() error { return e.err }

func withKind(kind int, err error) error { return &kindError{kind: kind, err: err} }

func withKindDetail(kind int, detail string, err error) error {
	return &kindError{kind: kind, detail: detail, err: err}
}

// kindOf reports the outermost kind attached to err.
func kindOf(err error) (int, string) {
	if err == nil {
		return FailureNone, ""
	}
	var k *kindError
	if errors.As(err, &k) {
		return k.kind, k.detail
	}
	return FailureOther, ""
}

// lastFailure records the kind of the most recent call's outcome. It
// has its own lock so that reading it never waits on a call in
// progress.
type lastFailure struct {
	mu     sync.Mutex
	kind   int
	detail string
}

func (l *lastFailure) record(err error) {
	kind, detail := kindOf(err)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kind, l.detail = kind, detail
}

func (l *lastFailure) get() (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.kind, l.detail
}
