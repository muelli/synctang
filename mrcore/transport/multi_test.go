// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
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
