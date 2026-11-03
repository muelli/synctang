// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, header string
		want         time.Duration
	}{
		{"seconds", "350", 350 * time.Second},
		{"seconds with space", " 1200 ", 1200 * time.Second},
		{"zero", "0", 0},
		{"date in the future", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"date in the past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"absent", "", DefaultRetryAfter},
		{"garbage", "soon", DefaultRetryAfter},
		{"negative", "-5", DefaultRetryAfter},
		{"fraction", "1.5", DefaultRetryAfter},
		{"huge number", "99999999999999999999", DefaultRetryAfter},
	} {
		if got := ParseRetryAfter(tc.header, now); got != tc.want {
			t.Errorf("%s: ParseRetryAfter(%q) = %v, want %v", tc.name, tc.header, got, tc.want)
		}
	}
}

func TestDialRetryDelaySequenceAndBounds(t *testing.T) {
	// With the middle of the jitter range the delay is the schedule itself.
	var got []time.Duration
	for n := 0; n < 8; n++ {
		got = append(got, dialRetryDelay(n, 0.5))
	}
	want := []time.Duration{3, 5, 8, 12, 15, 15, 15, 15}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("delay %d = %v, want %v (all: %v)", i, got[i], want[i]*time.Second, got)
		}
	}
	// The jitter stays within 20 percent either way, and nothing exceeds the cap.
	for n := 0; n < 8; n++ {
		base := dialRetryDelay(n, 0.5)
		for _, r := range []float64{0, 0.25, 0.75, 0.999999} {
			d := dialRetryDelay(n, r)
			if d > dialRetryMax {
				t.Errorf("delay %d (r=%v) = %v exceeds the cap %v", n, r, d, dialRetryMax)
			}
			if d < time.Duration(float64(base)*0.8)-time.Millisecond || d > time.Duration(float64(base)*1.2)+time.Millisecond {
				t.Errorf("delay %d (r=%v) = %v, more than 20 percent from %v", n, r, d, base)
			}
		}
	}
	if d := dialRetryDelay(0, 0); d != 2400*time.Millisecond {
		t.Errorf("shortest first delay = %v, want 2.4s", d)
	}
}

func TestBoundedRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		retryAfter, remained time.Duration
		want                 time.Duration
	}{
		{"as told", 20 * time.Second, time.Hour, 20 * time.Second},
		{"capped at a minute", 20 * time.Minute, time.Hour, time.Minute},
		{"capped by what is left", 20 * time.Second, 5 * time.Second, 5 * time.Second},
		{"nothing left", 20 * time.Second, 0, 0},
		{"no deadline", 20 * time.Second, -1, 20 * time.Second},
		{"zero", 0, time.Hour, 0},
	} {
		if got := boundedRetryAfter(tc.retryAfter, tc.remained); got != tc.want {
			t.Errorf("%s: boundedRetryAfter(%v, %v) = %v, want %v", tc.name, tc.retryAfter, tc.remained, got, tc.want)
		}
	}
}

func TestLookupAnswering429IsATypedErrorWithRetryAfter(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "350")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	_, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?insecure")
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("lookupRecord error = %v, want a *RateLimitedError", err)
	}
	if limited.RetryAfter != 350*time.Second {
		t.Errorf("RetryAfter = %v, want 350s", limited.RetryAfter)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
}

// fakeTime stands in for time in a dial: its sleeper takes no time, adds what it was asked
// for to a clock, and ends the dial (cancels its context) once the clock has passed budget,
// the way the caller's deadline would.
type fakeTime struct {
	mu      sync.Mutex
	elapsed time.Duration
	slept   []time.Duration
	budget  time.Duration
	cancel  context.CancelFunc
}

func (f *fakeTime) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.elapsed += d
	f.slept = append(f.slept, d)
	over := f.elapsed >= f.budget
	f.mu.Unlock()
	if over {
		f.cancel()
	}
	return ctx.Err()
}

func (f *fakeTime) now() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.elapsed
}

// dialUntilBudget runs a dial of peer against a discovery server whose answer is decided by
// answer (called with the number of the request, counted from 1, and the fake clock), with
// everything but the lookups made instant. It returns the fake clock for what was slept.
func dialUntilBudget(t *testing.T, budget time.Duration, ctxTimeout time.Duration, answer func(w http.ResponseWriter, n int, f *fakeTime)) (*fakeTime, *discoveryServer, []DialPhase, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if ctxTimeout > 0 {
		var c2 context.CancelFunc
		ctx, c2 = context.WithTimeout(ctx, ctxTimeout)
		defer c2()
	}
	defer cancel()
	f := &fakeTime{budget: budget, cancel: cancel}
	var n atomic.Int32
	srv := &discoveryServer{}
	srv.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.requests.Add(1)
		answer(w, int(n.Add(1)), f)
	}))
	t.Cleanup(srv.Close)

	var phases []DialPhase
	var pmu sync.Mutex
	tr := &SyncthingRelay{
		DiscoveryURL: srv.URL + "/v2/?id=" + srv.id(),
		sleep:        f.sleep,
		jitter:       func() float64 { return 0.5 },
		OnDialPhase: func(_ Route, p DialPhase) {
			pmu.Lock()
			phases = append(phases, p)
			pmu.Unlock()
		},
	}
	_, err := tr.dialRelay(ctx, someDeviceID)
	if err == nil {
		t.Fatal("dialRelay succeeded against a discovery server with no machine")
	}
	return f, srv, phases, err
}

func notFound(w http.ResponseWriter, _ int, _ *fakeTime) {
	w.WriteHeader(http.StatusNotFound)
}

func TestDialForANinetySecondBudgetLooksTheMachineUpOnlyAFewTimes(t *testing.T) {
	f, srv, phases, _ := dialUntilBudget(t, 90*time.Second, 0, notFound)
	got := int(srv.requests.Load())
	t.Logf("lookups in a 90 s dial against a machine that never appears: %d (waits %v)", got, f.slept)
	if got > 12 {
		t.Errorf("%d lookups in 90 s, want at most 12", got)
	}
	if got < 5 {
		t.Errorf("only %d lookups in 90 s: the dial is giving up on the machine too early", got)
	}
	lookups := 0
	for _, p := range phases {
		if p == PhaseLookup {
			lookups++
		}
	}
	if lookups != got {
		t.Errorf("reported the looking-up stage %d times for %d lookups", lookups, got)
	}
}

func TestDialFindsAMachineThatAppearsFiveSecondsIn(t *testing.T) {
	var firstWithAddresses time.Duration = -1
	_, srv, _, _ := dialUntilBudget(t, 40*time.Second, 0, func(w http.ResponseWriter, _ int, f *fakeTime) {
		if f.now() < 5*time.Second {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if firstWithAddresses < 0 {
			firstWithAddresses = f.now()
		}
		// A direct address nothing listens on: the lookup is what is being timed.
		_ = json.NewEncoder(w).Encode(map[string]any{"seen": time.Now().UTC().Format(time.RFC3339), "addresses": []string{"tcp://127.0.0.1:1"}})
	})
	if firstWithAddresses < 0 {
		t.Fatalf("never asked again after %d requests", srv.requests.Load())
	}
	t.Logf("machine appeared at 5 s; first lookup that could see it at %v", firstWithAddresses)
	if firstWithAddresses > 9*time.Second {
		t.Errorf("first retry that saw the machine came %v in, want within 9s", firstWithAddresses)
	}
}

func TestDialHonoursRetryAfterOf429AndRecovers(t *testing.T) {
	var firstSleep time.Duration
	var seenAfter429 atomic.Bool
	f, srv, phases, _ := dialUntilBudget(t, 60*time.Second, 0, func(w http.ResponseWriter, n int, f *fakeTime) {
		if n == 1 {
			w.Header().Set("Retry-After", "20")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		if n == 2 {
			firstSleep = f.now()
			seenAfter429.Store(true)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"seen": time.Now().UTC().Format(time.RFC3339), "addresses": []string{"tcp://127.0.0.1:1"}})
	})
	if !seenAfter429.Load() {
		t.Fatal("never looked up again after the 429")
	}
	if firstSleep != 20*time.Second {
		t.Errorf("second lookup came %v after the first, want the 20s Retry-After", firstSleep)
	}
	var connects int
	for _, p := range phases {
		if p == PhaseConnect {
			connects++
		}
	}
	if connects == 0 {
		t.Error("never got as far as connecting after the 429: the dial did not recover")
	}
	t.Logf("requests in 60 s with a 429 and Retry-After 20: %d (waits %v)", srv.requests.Load(), f.slept)
}

func TestDialWaitsNoLongerThanAMinuteOrWhatIsLeftForARetryAfter(t *testing.T) {
	// Retry-After of an hour, but the dial has ten seconds left.
	f, srv, _, _ := dialUntilBudget(t, 5*time.Second, 10*time.Second, func(w http.ResponseWriter, _ int, _ *fakeTime) {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	if len(f.slept) == 0 || f.slept[0] > 10*time.Second || f.slept[0] <= 5*time.Second {
		t.Errorf("waited %v after a 429 with 10s left, want what is left (more than the 3s backoff, at most 10s)", f.slept)
	}
	if srv.requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", srv.requests.Load())
	}
	// And with plenty of time, an hour is a minute.
	f, _, _, _ = dialUntilBudget(t, 3*time.Minute, 0, func(w http.ResponseWriter, _ int, _ *fakeTime) {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	if f.slept[0] != time.Minute {
		t.Errorf("waited %v after a Retry-After of an hour, want 1m", f.slept[0])
	}
}
