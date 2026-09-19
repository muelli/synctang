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

	raceCtx, cancel := context.WithCancel(ctx)

	results := make(chan multiResult, len(m.Transports))
	for _, tr := range m.Transports {
		tr := tr
		go func() {
			conn, err := attempt(raceCtx, tr)
			results <- multiResult{conn, err}
		}()
	}

	var errs []error
	for i := 0; i < len(m.Transports); i++ {
		r := <-results
		if r.err == nil {
			cancel()
			if remaining := len(m.Transports) - i - 1; remaining > 0 {
				go closeLateArrivals(results, remaining)
			}
			return r.conn, nil
		}
		errs = append(errs, r.err)
	}
	cancel()
	return nil, errors.Join(errs...)
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
