// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"fmt"
)

// Multi races several Transports against each other for both Listen
// and Dial: whichever completes successfully first wins, and the rest
// are cancelled. This is what lets unlock keep working with no
// Internet route at all, without either side having to guess in
// advance whether one is available: a LocalDiscovery transport (same
// LAN, no Internet needed) and a SyncthingRelay transport (anywhere,
// needs the Internet) can simply both be tried at once.
type Multi struct {
	Transports []Transport
}

type multiResult struct {
	conn Conn
	err  error
	// index identifies which transport produced this result, so the
	// winner's own context can be spared when the losers are cancelled.
	index int
}

// Dial implements Transport.
func (m *Multi) Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	return m.race(ctx, func(ctx context.Context, tr Transport) (Conn, error) {
		return tr.Dial(ctx, opts)
	})
}

// Listen implements Transport.
func (m *Multi) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	return m.race(ctx, func(ctx context.Context, tr Transport) (Conn, error) {
		return tr.Listen(ctx, opts)
	})
}

// race runs attempt against every configured Transport concurrently,
// under a context shared between them so that cancelling it (once one
// succeeds) stops the rest promptly rather than leaving them running
// to their own timeout. If every attempt fails, the returned error
// joins all of them, so a caller (or a human reading a log) sees why
// each path failed rather than only the last one to report in.
func (m *Multi) race(ctx context.Context, attempt func(context.Context, Transport) (Conn, error)) (Conn, error) {
	if len(m.Transports) == 0 {
		return nil, fmt.Errorf("transport: Multi has no transports configured")
	}

	// One context per attempt, rather than one shared one.
	//
	// The winner's context must outlive the race. SyncthingRelay builds
	// its relay client, its control connection and its announce loop on
	// the context it is handed, and has to keep them alive: the relay
	// server drops every session belonging to a device the instant that
	// device's control connection disconnects. Cancelling the winner
	// therefore does not tidy up a finished attempt, it destroys the
	// session that attempt just produced, and both ends read EOF on a
	// connection that completed its handshake a moment earlier.
	//
	// A shared raceCtx, cancelled as soon as a winner appeared, is what
	// this used to do. Nothing offline could see it: LocalDiscovery has
	// no control connection to lose and a DirectAddr listener has no
	// relay at all, so every test passed while unlocking over the real
	// relay pool failed nearly every time.
	cancels := make([]context.CancelFunc, len(m.Transports))
	results := make(chan multiResult, len(m.Transports))
	for i, tr := range m.Transports {
		i, tr := i, tr
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		cancels[i] = cancelAttempt
		go func() {
			conn, err := attempt(attemptCtx, tr)
			results <- multiResult{conn: conn, err: err, index: i}
		}()
	}

	cancelAllExcept := func(winner int) {
		for i, cancel := range cancels {
			if i != winner {
				cancel()
			}
		}
	}

	var errs []error
	for i := 0; i < len(m.Transports); i++ {
		r := <-results
		if r.err == nil {
			cancelAllExcept(r.index)
			if remaining := len(m.Transports) - i - 1; remaining > 0 {
				go closeLateArrivals(results, remaining)
			}
			// The winner is released when its connection is closed,
			// which is when the session it produced is genuinely
			// finished with, and not before.
			return &cancelOnCloseConn{Conn: r.conn, cancel: cancels[r.index]}, nil
		}
		errs = append(errs, r.err)
	}
	cancelAllExcept(-1)
	return nil, errors.Join(errs...)
}

// cancelOnCloseConn releases the winning attempt's context when the
// connection it produced is closed, so that the transport's own
// background machinery lives exactly as long as the connection does.
type cancelOnCloseConn struct {
	Conn
	cancel context.CancelFunc
}

func (c *cancelOnCloseConn) Close() error {
	err := c.Conn.Close()
	c.cancel()
	return err
}

// closeLateArrivals drains the results still outstanding after race
// has already returned a winner, closing any connection among them.
// Cancelling raceCtx only stops a losing attempt that has not yet
// finished; one already in the middle of a TLS handshake, or one
// whose Transport simply does not check ctx after committing to an
// operation, can still report success afterwards. That is a second
// real, authenticated connection to the same peer nobody is going to
// use, and leaving it open leaks a live session on both ends rather
// than merely a goroutine, so it must be closed, not dropped.
func closeLateArrivals(results chan multiResult, n int) {
	for i := 0; i < n; i++ {
		r := <-results
		if r.err == nil && r.conn != nil {
			r.conn.Close()
		}
	}
}
