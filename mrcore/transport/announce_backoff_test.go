// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The public discovery server answers an announcer that is too eager with HTTP 429 and a Retry-After of minutes.
// The announce loop used to try again every two seconds on every failure, which is how a machine waiting at its
// prompt kept being told "429" (observed: the same warning, over and over, from the unlocker's journal).

// limitedServer answers every announcement with the status given and, for a 429, the Retry-After given, and
// counts them.
func limitedServer(t *testing.T, status int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestAnAnnounceAnsweredWith429IsARateLimitWithTheTimeTheServerAskedFor(t *testing.T) {
	srv, _ := limitedServer(t, http.StatusTooManyRequests, "120")
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL}}
	relay.httpClient = srv.Client()

	err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"})
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("a 429 was not reported as a rate limit: %v", err)
	}
	if limited.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %s, want the 120 s the server asked for", limited.RetryAfter)
	}
}

func TestANoteOfARateLimitKeepsAnnouncementsAwayFromThatServerUntilItIsOver(t *testing.T) {
	srv, hits := limitedServer(t, http.StatusTooManyRequests, "120")
	now := time.Date(2026, 11, 11, 21, 0, 0, 0, time.UTC)
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL}, now: func() time.Time { return now }}
	relay.httpClient = srv.Client()
	addrs := []string{"tcp://127.0.0.1:22000"}

	_ = relay.announceOnce(context.Background(), addrs)
	now = now.Add(30 * time.Second)
	err := relay.announceOnce(context.Background(), addrs)
	if hits.Load() != 1 {
		t.Errorf("the server was asked again after %d requests, 30 s into a 120 s wait", hits.Load())
	}
	var limited *RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter != 90*time.Second {
		t.Errorf("what is left of the wait should be said: %v", err)
	}

	now = now.Add(91 * time.Second)
	_ = relay.announceOnce(context.Background(), addrs)
	if hits.Load() != 2 {
		t.Errorf("after the wait it should ask again: %d requests", hits.Load())
	}
}

func TestOneLimitedServerDoesNotStopTheOthers(t *testing.T) {
	limited, limitedHits := limitedServer(t, http.StatusTooManyRequests, "600")
	good, goodHits := limitedServer(t, http.StatusNoContent, "")
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{limited.URL, good.URL}}
	relay.httpClient = good.Client()

	for i := 0; i < 3; i++ {
		if err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"}); err != nil {
			t.Fatalf("announce %d: %v", i, err)
		}
	}
	if limitedHits.Load() != 1 || goodHits.Load() != 3 {
		t.Errorf("limited server asked %d times (want 1), good one %d (want 3)", limitedHits.Load(), goodHits.Load())
	}
}

func TestAWaitThatIsAbsurdlyLongIsCut(t *testing.T) {
	srv, hits := limitedServer(t, http.StatusTooManyRequests, "4000000")
	now := time.Date(2026, 11, 11, 21, 0, 0, 0, time.UTC)
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL}, now: func() time.Time { return now }}
	relay.httpClient = srv.Client()
	_ = relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"})
	now = now.Add(announceRetryAfterMax + time.Second)
	_ = relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"})
	if hits.Load() != 2 {
		t.Errorf("a machine must not stay silent for weeks because a server said so: %d requests", hits.Load())
	}
}

func TestTheAnnounceLoopStaysAwayForAsLongAsTheServerAsked(t *testing.T) {
	srv, hits := limitedServer(t, http.StatusTooManyRequests, "3600")
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL}, AnnounceInterval: 5 * time.Millisecond}
	relay.httpClient = srv.Client()
	stop := relay.announceLoop(context.Background(), func() []string { return []string{"tcp://127.0.0.1:22000"} })
	time.Sleep(200 * time.Millisecond) // forty ticks
	stop()
	if hits.Load() != 1 {
		t.Errorf("%d announcements in 200 ms after a 429 with Retry-After: 3600, want 1", hits.Load())
	}
}

func TestTheAnnounceLoopBacksOffAfterAFailureThatIsNotARateLimit(t *testing.T) {
	srv, hits := limitedServer(t, http.StatusInternalServerError, "")
	relay := &SyncthingRelay{
		Cert: testCert(t), AnnounceURLs: []string{srv.URL}, AnnounceInterval: 5 * time.Millisecond,
		announceBackoffBase: 40 * time.Millisecond, jitter: func() float64 { return 0.5 },
	}
	relay.httpClient = srv.Client()
	stop := relay.announceLoop(context.Background(), func() []string { return []string{"tcp://127.0.0.1:22000"} })
	time.Sleep(230 * time.Millisecond)
	stop()
	// Attempts at about 0, 40, 120 and 280 ms: three or four, not the forty-six that one per tick would be.
	if n := hits.Load(); n < 2 || n > 5 {
		t.Errorf("%d announcements in 230 ms with a 40 ms doubling backoff, want 3 or 4", n)
	}
}

func TestARateLimitIsLoggedOnceWithTheWaitNotOnEveryTick(t *testing.T) {
	srv, _ := limitedServer(t, http.StatusTooManyRequests, "3600")
	var mu sync.Mutex
	var messages []string
	relay := &SyncthingRelay{
		Cert: testCert(t), AnnounceURLs: []string{srv.URL}, AnnounceInterval: 5 * time.Millisecond,
		Logger: slog.New(lockedRecorder{messages: &messages, mu: &mu}),
	}
	relay.httpClient = srv.Client()
	stop := relay.announceLoop(context.Background(), func() []string { return []string{"tcp://127.0.0.1:22000"} })
	time.Sleep(100 * time.Millisecond)
	stop()
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Errorf("%d log lines for one rate limit: %v", len(messages), messages)
	}
}

// lockedRecorder is recordingHandler for a log that the loop writes from its own goroutine while the test reads it.
type lockedRecorder struct {
	messages *[]string
	mu       *sync.Mutex
}

func (h lockedRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h lockedRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.messages = append(*h.messages, r.Message)
	return nil
}
func (h lockedRecorder) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h lockedRecorder) WithGroup(_ string) slog.Handler      { return h }

func TestTheBackoffGrowsAndIsCapped(t *testing.T) {
	prev := time.Duration(0)
	for failures := 1; failures <= 12; failures++ {
		d := announceBackoff(failures, 2*time.Second, 0.5)
		if d < prev || d > announceBackoffMax {
			t.Errorf("failure %d: %s after %s (max %s)", failures, d, prev, announceBackoffMax)
		}
		prev = d
	}
	if announceBackoff(1, 2*time.Second, 0.5) != 2*time.Second || announceBackoff(40, 2*time.Second, 0.5) != announceBackoffMax {
		t.Errorf("first %s, last %s", announceBackoff(1, 2*time.Second, 0.5), announceBackoff(40, 2*time.Second, 0.5))
	}
}
