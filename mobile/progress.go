// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"sync"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
)

// How far a Session or a Pairer has got, so that the app can say what it is
// doing while it waits instead of showing a spinner and one fixed sentence.
// Session.Stage and Pairer.Stage report one of these; the numbers are part of the
// contract with the app and must not be reused for something else. They go up
// through one attempt, and a new attempt (a retry) starts again from where its
// route starts.
//
// Reading them never waits for a call in progress: Connect holds the session's
// lock while it dials, so progress has a lock of its own.
const (
	// StageNone: nothing has been asked of this object yet.
	StageNone = 0
	// StageStarting: Connect or Pair has been called and nothing has
	// happened on the network yet.
	StageStarting = 1
	// StageLookingUp: finding out where the machine is, by asking the
	// discovery server and by listening on the local network.
	StageLookingUp = 2
	// StageConnecting: reaching the machine's address, through a relay or
	// directly.
	StageConnecting = 3
	// StageCheckingIdentity: connected; the TLS handshake that checks the
	// machine's Device ID is running.
	StageCheckingIdentity = 4
	// StageWaitingForRequest: the machine's identity is confirmed and the
	// phone is waiting for it to say hello and ask for an answer.
	StageWaitingForRequest = 5
	// StageRequestReady: the machine's request has arrived and is waiting
	// for this phone's owner to approve it. Not a busy stage: the app is
	// showing its approve screen.
	StageRequestReady = 6
	// StageSendingAnswer: the answer is being sent to the machine.
	StageSendingAnswer = 7
	// StageWaitingForVerdict: the answer is sent; the machine is checking
	// that it opens the volume.
	StageWaitingForVerdict = 8
	// StageUnlocked: the machine reported that it used the answer.
	StageUnlocked = 9
	// StageSendingProof: (pairing) connected and checked; the phone is
	// sending the pairing code's proof and its public key.
	StageSendingProof = 10
	// StageWaitingForOperator: (pairing) the proof is sent; the machine
	// is checking it and then asking the person at its console to confirm.
	// This is the long wait, and it is a person deciding.
	StageWaitingForOperator = 11
	// StageEnrolled: (pairing) the machine enrolled this phone.
	StageEnrolled = 12
)

// Which way the machine was reached, once that is known: Session.Route and
// Pairer.Route report one of these.
const (
	// RouteUnknown: not known yet.
	RouteUnknown = 0
	// RouteRelay: through a relay, found by global discovery.
	RouteRelay = 1
	// RouteLocal: over the local network, found by multicast.
	RouteLocal = 2
	// RouteDirect: a plain address, given by the caller or by discovery.
	RouteDirect = 3
)

// progress is the shared record behind Stage, Attempt and Route.
type progress struct {
	mu       sync.Mutex
	stage    int
	attempt  int
	route    int
	legRoute map[int]transport.Route
	stages   []int // every distinct stage, in order, for the tests

	connectedAt time.Time // when the last successful dial returned
}

// markConnected records that the connection to the machine has just been made.
func (p *progress) markConnected() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connectedAt = time.Now()
}

func (p *progress) connectedForMillis() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connectedAt.IsZero() {
		return 0
	}
	return int(time.Since(p.connectedAt).Milliseconds())
}

func (p *progress) setLocked(stage int) {
	if stage == p.stage {
		return
	}
	p.stage = stage
	p.stages = append(p.stages, stage)
}

// advance moves to stage if it is later than the current one. The legs of a
// raced dial report concurrently, and a slow leg's early phase must not pull the
// stage back.
func (p *progress) advance(stage int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if stage > p.stage {
		p.setLocked(stage)
	}
}

// beginAttempt starts attempt n+1 at stage first, which may be lower than where
// the last attempt got to.
func (p *progress) beginAttempt(first int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempt++
	p.legRoute = map[int]transport.Route{}
	p.setLocked(first)
}

func (p *progress) snapshot() (stage, attempt, route int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stage, p.attempt, p.route
}

func (p *progress) history() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.stages...)
}

// dialHook is the OnDialPhase of the transport at index leg.
func (p *progress) dialHook(leg int) transport.DialProgress {
	return func(route transport.Route, phase transport.DialPhase) {
		p.mu.Lock()
		if p.legRoute == nil {
			p.legRoute = map[int]transport.Route{}
		}
		p.legRoute[leg] = route
		p.mu.Unlock()
		switch phase {
		case transport.PhaseLookup:
			p.advance(StageLookingUp)
		case transport.PhaseConnect:
			p.advance(StageConnecting)
		case transport.PhaseHandshake:
			p.advance(StageCheckingIdentity)
		}
	}
}

// won records which leg's connection was used, and so which route it was.
func (p *progress) won(leg int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.legRoute[leg] {
	case transport.RouteRelay:
		p.route = RouteRelay
	case transport.RouteLocal:
		p.route = RouteLocal
	case transport.RouteDirect:
		p.route = RouteDirect
	}
}

// firstStage is where an attempt starts: at the lookup, or, with an address
// given, at connecting to it.
func firstStage(directAddr string) int {
	if directAddr != "" {
		return StageConnecting
	}
	return StageLookingUp
}

// Stage is how far this session has got; see the Stage constants.
func (s *Session) Stage() int { st, _, _ := s.progress.snapshot(); return st }

// Attempt counts the dials made so far, from 1 once Connect has begun. More than
// one means the first did not get through and it is trying again.
func (s *Session) Attempt() int { _, a, _ := s.progress.snapshot(); return a }

// Route is which way the machine was reached (a Route constant), or RouteUnknown.
func (s *Session) Route() int { _, _, r := s.progress.snapshot(); return r }

// Stage is how far this pairing has got; see the Stage constants.
func (p *Pairer) Stage() int { st, _, _ := p.progress.snapshot(); return st }

// Attempt counts the dials made so far, from 1 once Pair has begun.
func (p *Pairer) Attempt() int { _, a, _ := p.progress.snapshot(); return a }

// Route is which way the machine was reached (a Route constant), or RouteUnknown.
func (p *Pairer) Route() int { _, _, r := p.progress.snapshot(); return r }

// ConnectedForMillis is how long ago the connection to the machine was made, in
// milliseconds, or 0 if it has not been. The machine's side of the session, and a
// relay's idle clock, started then, which is before the request arrived if the
// machine was slow to send it, so the app counts the time left to approve from here.
func (s *Session) ConnectedForMillis() int { return s.progress.connectedForMillis() }
