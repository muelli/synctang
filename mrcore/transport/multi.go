// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Multi races several Transports against each other for both Listen
// and Dial: whichever completes successfully first wins, and the rest
// are cancelled. This is what lets unlock keep working with no
// Internet route at all, without either side having to guess in
// advance whether one is available: a LocalDiscovery transport (same
// LAN, no Internet needed) and a SyncthingRelay transport (anywhere,
// needs the Internet) can simply both be tried at once.
type Multi struct {
	// RetryInterval is how long race waits before restarting an
	// attempt that failed. Zero means defaultMultiRetryInterval.
	RetryInterval time.Duration

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
	return m.race(ctx, false, func(ctx context.Context, tr Transport) (Conn, error) {
		return tr.Dial(ctx, opts)
	})
}

// Listen implements Transport.
func (m *Multi) Listen(ctx context.Context, opts ListenOptions) (Conn, error) {
	// Listening retries a failed leg: a machine waiting at its LUKS
	// prompt has nothing better to do, and the alternative is going
	// dark for the rest of the boot the first time a stranger connects.
	return m.race(ctx, true, func(ctx context.Context, tr Transport) (Conn, error) {
		return tr.Listen(ctx, opts)
	})
}

// race runs attempt against every configured Transport concurrently,
// under a context shared between them so that cancelling it (once one
// succeeds) stops the rest promptly rather than leaving them running
// to their own timeout. If every attempt fails, the returned error
// joins all of them, so a caller (or a human reading a log) sees why
// each path failed rather than only the last one to report in.
func (m *Multi) race(ctx context.Context, retryFailed bool, attempt func(context.Context, Transport) (Conn, error)) (Conn, error) {
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
	// A failed attempt is restarted rather than merely recorded. The
	// legs are not symmetrical: LocalDiscovery blocks until somebody
	// dials, which on a machine waiting at its LUKS prompt may be
	// hours or never, while a relay listen ends whenever an attempt
	// does, including when a stranger connects and is refused. Waiting
	// for every leg to report before returning therefore meant waiting
	// for a LocalDiscovery result that was never coming: the agent
	// never retried, never re-announced, and the machine went dark for
	// the rest of the boot while still showing "waiting for a key
	// holder" on its console. Anyone could cause it on purpose, since
	// Device IDs are published on discovery and one refused connection
	// was enough.
	var mu sync.Mutex
	cancels := make(map[int]context.CancelFunc, len(m.Transports))
	outstanding := 0
	results := make(chan multiResult, len(m.Transports))

	launch := func(i int) {
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		mu.Lock()
		cancels[i] = cancelAttempt
		outstanding++
		mu.Unlock()
		go func() {
			conn, err := attempt(attemptCtx, m.Transports[i])
			results <- multiResult{conn: conn, err: err, index: i}
		}()
	}

	cancelAllExcept := func(winner int) {
		mu.Lock()
		defer mu.Unlock()
		for i, cancel := range cancels {
			if i != winner {
				cancel()
			}
		}
	}

	for i := range m.Transports {
		launch(i)
	}

	// lastErr per transport rather than every error ever seen: a wait
	// of hours would otherwise accumulate one error per retry for as
	// long as it lasted.
	lastErr := make(map[int]error, len(m.Transports))
	for {
		select {
		case <-ctx.Done():
			cancelAllExcept(-1)
			errs := make([]error, 0, len(lastErr)+1)
			for _, err := range lastErr {
				errs = append(errs, err)
			}
			errs = append(errs, ctx.Err())
			return nil, errors.Join(errs...)

		case r := <-results:
			mu.Lock()
			outstanding--
			stillRunning := outstanding
			mu.Unlock()

			if r.err == nil {
				cancelAllExcept(r.index)
				if stillRunning > 0 {
					go closeLateArrivals(results, stillRunning)
				}
				mu.Lock()
				winnerCancel := cancels[r.index]
				mu.Unlock()
				// The winner is released when its connection is
				// closed, which is when the session it produced is
				// genuinely finished with, and not before.
				return &cancelOnCloseConn{Conn: r.conn, cancel: winnerCancel}, nil
			}

			lastErr[r.index] = r.err
			mu.Lock()
			if cancel, ok := cancels[r.index]; ok {
				cancel()
				delete(cancels, r.index)
			}
			finished := len(lastErr) == len(m.Transports) && outstanding == 0
			mu.Unlock()

			if !retryFailed {
				// Dial keeps its original contract: report failure
				// once every leg has failed, and let the caller decide
				// whether to try again. Its callers already retry, and
				// an unbounded Dial that never reports anything is a
				// worse footgun than a prompt error.
				if finished {
					cancelAllExcept(-1)
					errs := make([]error, 0, len(lastErr))
					for _, err := range lastErr {
						errs = append(errs, err)
					}
					return nil, errors.Join(errs...)
				}
				continue
			}

			go func(i int) {
				select {
				case <-ctx.Done():
				case <-time.After(m.retryInterval()):
					launch(i)
				}
			}(r.index)
		}
	}
}

// defaultMultiRetryInterval is how long race waits before restarting a
// failed attempt: long enough not to spin against a relay pool that is
// refusing, short enough that a machine is off the air for about as
// long as it takes to notice.
const defaultMultiRetryInterval = 2 * time.Second

func (m *Multi) retryInterval() time.Duration {
	if m.RetryInterval != 0 {
		return m.RetryInterval
	}
	return defaultMultiRetryInterval
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
