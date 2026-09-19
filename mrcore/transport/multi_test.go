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
