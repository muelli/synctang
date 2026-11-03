// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The public discovery server is shared, and it answers a client that asks too often with
// HTTP 429 and a Retry-After of minutes (350 to 1200 seconds, measured). Everything that
// looks a machine up has to ask sparingly and has to stop when told to: that is this file.

// DefaultRetryAfter is what a 429 with a Retry-After that cannot be read is taken to mean.
const DefaultRetryAfter = 60 * time.Second

// RateLimitedError is a discovery server telling the caller to ask less often (HTTP 429).
// It is not a failure to reach the server, and callers that tell the two apart should.
type RateLimitedError struct {
	// RetryAfter is how long the server asked the caller to wait, as parsed (not bounded):
	// DefaultRetryAfter when the header is missing or unreadable.
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("discovery lookup returned HTTP 429: asked to wait %s", e.RetryAfter.Round(time.Second))
}

// ParseRetryAfter reads a Retry-After header, which is either a whole number of seconds or
// an HTTP date (relative to now). A date already past is 0. Anything else, including a
// missing header, a negative or fractional number or one too large to be one, is
// DefaultRetryAfter: a server that says "wait" in a way that cannot be read still said wait.
func ParseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return DefaultRetryAfter
	}
	if secs, err := strconv.ParseUint(header, 10, 32); err == nil {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return DefaultRetryAfter
}

// The delay between a dial's lookups. The first retry is quick, so a machine that has just
// come up is found promptly, and the delays then grow to a cap, so a machine that is not
// coming up is not asked about every few seconds for the whole budget (90 s is about ten
// lookups, where a fixed 3 s was thirty).
var dialRetrySchedule = [...]time.Duration{3 * time.Second, 5 * time.Second, 8 * time.Second, 12 * time.Second}

const (
	// dialRetryMax is the longest wait between two lookups, jitter included.
	dialRetryMax = 15 * time.Second
	// dialRetryJitter is how far, as a fraction, a delay moves either way, so that many
	// phones and CLIs that started together do not keep asking together.
	dialRetryJitter = 0.2
	// dialRetryAfterMax bounds how long a dial honours a Retry-After: its own budget is
	// short, and a longer wait is a failed dial, which the person can retry.
	dialRetryAfterMax = time.Minute
)

// dialRetryDelay is the wait after the retry-th failed attempt (counted from 0): 3, 5, 8, 12,
// then 15 s, each moved by up to 20 percent according to r in [0, 1), and never above 15 s.
func dialRetryDelay(retry int, r float64) time.Duration {
	base := dialRetryMax
	if retry >= 0 && retry < len(dialRetrySchedule) {
		base = dialRetrySchedule[retry]
	}
	d := time.Duration(float64(base) * (1 + dialRetryJitter*(2*r-1)))
	return min(d, dialRetryMax)
}

// boundedRetryAfter is how long a dial waits for a Retry-After: as told, but no longer than
// dialRetryAfterMax and no longer than remaining, the time left in the dial (negative when
// the dial has no deadline).
func boundedRetryAfter(retryAfter, remaining time.Duration) time.Duration {
	d := min(max(retryAfter, 0), dialRetryAfterMax)
	if remaining >= 0 {
		d = min(d, remaining)
	}
	return d
}

func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
