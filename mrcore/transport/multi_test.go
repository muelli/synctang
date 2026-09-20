// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTransport is a stub Transport for exercising Multi's racing
// behaviour without any real network, so these tests run fast and
// deterministically regardless of the host's multicast or Internet
// access.
type fakeTransport struct {
	delay   time.Duration
	conn    Conn
	err     error
	started chan struct{} // closed once Dial/Listen is actually called, for tests that need to know an attempt started
	seenCtx context.Context
}

func (f *fakeTransport) attempt(ctx context.Context) (Conn, error) {
	if f.started != nil {
		close(f.started)
	}
	f.seenCtx = ctx
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.conn, f.err
}

func (f *fakeTransport) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	return f.attempt(ctx)
}

func (f *fakeTransport) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	return f.attempt(ctx)
}

type fakeConn struct {
	Conn
	id string
}

func (f *fakeConn) PeerID() string { return f.id }

func (f *fakeConn) Close() error { return nil }

// trackedConn is a fakeConn that records whether Close was called,
// safe for concurrent use: the background drain that closes a late
// arrival runs on its own goroutine, separate from whichever
// goroutine later inspects the result.
type trackedConn struct {
	fakeConn
	mu     sync.Mutex
	closed bool
}

func (c *trackedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *trackedConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// slowSuccessTransport succeeds unconditionally after delay,
// regardless of ctx: a real Transport's Dial or Listen does not
// necessarily notice cancellation once it is already committed to an
// in-flight operation (a TLS handshake already under way runs to
// completion on its own; context cancellation only stops work that
// has not started yet), so a losing attempt racing against a faster
// winner can still report success after the race is already decided.
type slowSuccessTransport struct {
	delay time.Duration
	conn  Conn
}

func (s *slowSuccessTransport) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	time.Sleep(s.delay)
	return s.conn, nil
}

func (s *slowSuccessTransport) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	time.Sleep(s.delay)
	return s.conn, nil
}

// T2.x: the faster successful transport wins the race, even when a
// slower one has not yet failed (or would never fail at all).
func TestMultiDialReturnsFirstSuccess(t *testing.T) {
	fast := &fakeTransport{delay: 5 * time.Millisecond, conn: &fakeConn{id: "fast"}}
	slow := &fakeTransport{delay: time.Hour, conn: &fakeConn{id: "slow"}}
	m := &Multi{Transports: []Transport{slow, fast}}

	conn, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if conn.PeerID() != "fast" {
		t.Fatalf("expected the fast transport to win, got peer %q", conn.PeerID())
	}
}

// A transport that fails quickly must not sink the whole Dial if
// another one is still in flight and later succeeds.
func TestMultiDialSucceedsDespiteAnEarlyFailure(t *testing.T) {
	failFast := &fakeTransport{delay: time.Millisecond, err: errors.New("no route")}
	succeedSlower := &fakeTransport{delay: 20 * time.Millisecond, conn: &fakeConn{id: "eventual"}}
	m := &Multi{Transports: []Transport{failFast, succeedSlower}}

	conn, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if conn.PeerID() != "eventual" {
		t.Fatalf("expected the eventually-successful transport to win, got peer %q", conn.PeerID())
	}
}

// If every transport fails, the combined error must mention each of
// them, not just the last one to report: a human debugging "unlock
// didn't work" needs to see whether it was the LAN path, the Internet
// path, or both that failed.
func TestMultiDialJoinsAllErrorsWhenEveryoneFails(t *testing.T) {
	a := &fakeTransport{delay: time.Millisecond, err: errors.New("local: no announcement seen")}
	b := &fakeTransport{delay: time.Millisecond, err: errors.New("relay: no route to discovery")}
	m := &Multi{Transports: []Transport{a, b}}

	_, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"})
	if err == nil {
		t.Fatal("expected an error when every transport fails")
	}
	msg := err.Error()
	if !errors.Is(err, a.err) || !errors.Is(err, b.err) {
		t.Fatalf("expected the combined error to wrap both failures, got %q", msg)
	}
}

// Once one transport succeeds, the others' contexts must be
// cancelled, not left running to their own timeout: a losing
// LocalDiscovery.Dial should stop listening the moment SyncthingRelay
// wins, and vice versa.
func TestMultiDialCancelsTheLoser(t *testing.T) {
	winner := &fakeTransport{delay: 5 * time.Millisecond, conn: &fakeConn{id: "winner"}}
	loser := &fakeTransport{delay: time.Hour, err: errors.New("should have been cancelled first")}
	m := &Multi{Transports: []Transport{winner, loser}}

	if _, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"}); err != nil {
		t.Fatalf("Dial: %v", err)
	}

	select {
	case <-loser.seenCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("the losing transport's context was never cancelled")
	}
}

// Listen must race exactly the same way Dial does.
func TestMultiListenReturnsFirstSuccess(t *testing.T) {
	fast := &fakeTransport{delay: 5 * time.Millisecond, conn: &fakeConn{id: "fast"}}
	slow := &fakeTransport{delay: time.Hour, conn: &fakeConn{id: "slow"}}
	m := &Multi{Transports: []Transport{slow, fast}}

	conn, err := m.Listen(context.Background(), ListenOptions{})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if conn.PeerID() != "fast" {
		t.Fatalf("expected the fast transport to win, got peer %q", conn.PeerID())
	}
}

func TestMultiWithNoTransportsFails(t *testing.T) {
	m := &Multi{}
	if _, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"}); err == nil {
		t.Fatal("expected an error with no transports configured")
	}
}

// Found running unlock against a real deployment: a losing transport
// can still finish connecting after the race is already decided (its
// TLS handshake was already in flight when raceCtx was cancelled, and
// cancellation does not abort work already under way), producing a
// second real, authenticated connection to the same peer. Multi must
// close that connection rather than silently drop it: leaving it open
// leaks a live session on both ends, and on the machine side specifically,
// it means the agent can end up writing its whole recovery exchange
// into a connection the key holder is not reading from at all, which
// is exactly the "reading hello: EOF" failure this was found chasing.
func TestMultiDialClosesALateSecondSuccess(t *testing.T) {
	winner := &trackedConn{fakeConn: fakeConn{id: "winner"}}
	loser := &trackedConn{fakeConn: fakeConn{id: "loser"}}
	fast := &fakeTransport{delay: 5 * time.Millisecond, conn: winner}
	slow := &slowSuccessTransport{delay: 40 * time.Millisecond, conn: loser}
	m := &Multi{Transports: []Transport{fast, slow}}

	conn, err := m.Dial(context.Background(), DialOptions{PeerID: "whoever"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if conn.PeerID() != "winner" {
		t.Fatalf("expected the fast transport to win, got peer %q", conn.PeerID())
	}

	deadline := time.Now().Add(time.Second)
	for !loser.isClosed() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !loser.isClosed() {
		t.Fatal("a connection that finished after losing the race was never closed, leaking it")
	}
}

// The winner's context must outlive the race.
//
// SyncthingRelay builds its relay client, its control connection to
// the relay, and its announce loop on the context it is given, and
// keeps them alive because the relay server drops every session
// belonging to a device the moment that device's control connection
// goes away. Cancelling the winner's context therefore does not tidy
// up a finished attempt: it destroys the session that attempt just
// produced, and both ends read EOF on a connection that had completed
// its handshake a moment earlier.
//
// Found against a real machine over the real relay pool, and only
// visible there: LocalDiscovery has no control connection to lose, and
// a DirectAddr listener has no relay at all, so every unit test and
// every offline test passed throughout. The machine's own console
// gave it away, announcing "announce failed ... context canceled" at
// the exact instant a key holder connected.
func TestMultiDoesNotCancelTheWinner(t *testing.T) {
	winner := &fakeTransport{conn: &fakeConn{}}
	loser := &fakeTransport{delay: time.Hour}

	m := &Multi{Transports: []Transport{winner, loser}}
	conn, err := m.Dial(context.Background(), DialOptions{PeerID: "peer"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := winner.seenCtx.Err(); err != nil {
		t.Fatalf("the winning transport's context was cancelled (%v), which tears down the connection it just returned", err)
	}

	// The loser must still be stopped, or a Multi with a slow
	// transport leaks an attempt per unlock.
	waitFor(t, func() bool { return loser.seenCtx != nil && loser.seenCtx.Err() != nil },
		"the losing transport's context should have been cancelled")

	// Closing the connection is what finally releases the winner.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, func() bool { return winner.seenCtx.Err() != nil },
		"closing the connection should release the winner's context")
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// blockingThenFailing fails its first attempt and succeeds on the
// next, which is what a relay listen does when a stranger connects and
// is refused: that attempt is over, and the next one has every chance
// of working.
type retryingTransport struct {
	mu    sync.Mutex
	calls int
	conn  Conn
}

func (r *retryingTransport) attempt(ctx context.Context) (Conn, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	if n == 1 {
		return nil, errors.New("rejected an unauthorized peer")
	}
	return r.conn, nil
}

func (r *retryingTransport) Dial(ctx context.Context, _ DialOptions) (Conn, error) {
	return r.attempt(ctx)
}
func (r *retryingTransport) Listen(ctx context.Context, _ ListenOptions) (Conn, error) {
	return r.attempt(ctx)
}

// A failed leg must not end the race, and must not leave it waiting on
// the other leg for ever.
//
// The agent races LocalDiscovery against the relay. LocalDiscovery
// blocks until somebody dials, which on a machine waiting at its LUKS
// prompt may be hours or never. So when the relay leg failed, race sat
// waiting for a LocalDiscovery result that was never coming: the agent
// never retried, never re-announced, and the machine went dark for the
// rest of the boot while still displaying "waiting for a key holder".
//
// Anyone could cause that deliberately. Device IDs are published on
// discovery, and one connection from a stranger, refused exactly as it
// should be, was enough to take the machine off the air until somebody
// walked to it. Found by probing a machine that had been waiting
// happily for seven hours.
func TestMultiKeepsRacingAfterALegFails(t *testing.T) {
	flaky := &retryingTransport{conn: &fakeConn{id: "eventual"}}
	neverAnswers := &fakeTransport{delay: time.Hour}

	m := &Multi{Transports: []Transport{flaky, neverAnswers}, RetryInterval: 10 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := m.Listen(ctx, ListenOptions{})
	if err != nil {
		t.Fatalf("Listen should have succeeded on the retried leg, got %v", err)
	}
	defer conn.Close()
	if conn.PeerID() != "eventual" {
		t.Errorf("PeerID = %q, want the retried transport's connection", conn.PeerID())
	}
}

// With every leg failing and no deadline reached yet, race keeps
// trying rather than giving up: a machine at its LUKS prompt has
// nothing better to do, and the alternative is going dark.
func TestMultiGivesUpOnlyWhenTheContextDoes(t *testing.T) {
	alwaysFails := &fakeTransport{err: errors.New("no route")}
	alsoFails := &fakeTransport{err: errors.New("not found")}

	m := &Multi{Transports: []Transport{alwaysFails, alsoFails}, RetryInterval: 5 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := m.Listen(ctx, ListenOptions{}); err == nil {
		t.Fatal("expected an error once the context expired")
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("gave up after %s, should have kept retrying until the context expired", elapsed)
	}
}
