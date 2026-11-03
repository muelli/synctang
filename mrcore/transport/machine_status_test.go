// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/muelli/syncthing-socket/discovery"
)

func TestFormatAgeUsesTheUnitThatFits(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{-5 * time.Second, "0 s"},
		{0, "0 s"},
		{42 * time.Second, "42 s"},
		{59 * time.Second, "59 s"},
		{60 * time.Second, "1 min"},
		{150 * time.Second, "2 min"},
		{119*time.Minute + 59*time.Second, "119 min"},
		{120 * time.Minute, "2 h"},
		{47*time.Hour + 59*time.Minute, "47 h"},
		{48 * time.Hour, "2 days"},
		{10*24*time.Hour + time.Hour, "10 days"},
	} {
		if got := FormatAge(tc.age); got != tc.want {
			t.Errorf("FormatAge(%v) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

func TestStatusOfDecidesFromTheRecord(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	rec := func(seen time.Time) *discovery.Record {
		return &discovery.Record{Seen: seen, Addresses: []string{"tcp://x"}}
	}
	for _, tc := range []struct {
		name  string
		found bool
		rec   *discovery.Record
		state string
		age   time.Duration
	}{
		{"no record", false, nil, StatusAbsent, -1},
		{"no timestamp", true, rec(time.Time{}), StatusStale, -1},
		{"fresh", true, rec(now.Add(-10 * time.Second)), StatusWaiting, 10 * time.Second},
		{"at the limit", true, rec(now.Add(-WaitingRecordMaxAge)), StatusWaiting, WaitingRecordMaxAge},
		{"just past it", true, rec(now.Add(-WaitingRecordMaxAge - time.Second)), StatusStale, WaitingRecordMaxAge + time.Second},
		{"in the future", true, rec(now.Add(time.Hour)), StatusWaiting, 0},
	} {
		got := StatusOf(tc.found, tc.rec, now)
		if got.State != tc.state || got.Age != tc.age {
			t.Errorf("%s: StatusOf = %+v, want state %s age %v", tc.name, got, tc.state, tc.age)
		}
	}
}

func TestDialFailureHintSaysWhatDiscoveryKnows(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   MachineStatus
		want string
	}{
		{"waiting", MachineStatus{State: StatusWaiting, Age: 42 * time.Second},
			"The machine announced itself 42 s ago but did not answer. Try again. If it keeps failing, check its network."},
		{"stale in minutes", MachineStatus{State: StatusStale, Age: 47 * time.Minute},
			"The machine last announced itself 47 min ago, so it may not be at its prompt. Check the machine."},
		{"stale in hours", MachineStatus{State: StatusStale, Age: 5 * time.Hour},
			"The machine last announced itself 5 h ago, so it may not be at its prompt. Check the machine."},
		{"stale in days", MachineStatus{State: StatusStale, Age: 72 * time.Hour},
			"The machine last announced itself 3 days ago, so it may not be at its prompt. Check the machine."},
		{"stale with no timestamp", MachineStatus{State: StatusStale, Age: -1},
			"The machine announced itself at some point, so it may not be at its prompt. Check the machine."},
		{"absent", MachineStatus{State: StatusAbsent, Age: -1},
			"The machine has not announced itself. It may still be starting, switched off, or using another discovery server."},
		{"no network says nothing", MachineStatus{State: StatusNoNetwork, Age: -1}, ""},
		{"rate limited says nothing", MachineStatus{State: StatusRateLimited, Age: -1}, ""},
		{"unknown says nothing", MachineStatus{State: "SOMETHING", Age: -1}, ""},
	} {
		if got := DialFailureHint(tc.st); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestCheckMachineAsksOnceAndNeverErrors(t *testing.T) {
	srv := newDiscoveryServer(t, time.Now().Add(-30*time.Second).UTC().Format(time.RFC3339), "tcp://192.0.2.1:22000")
	got := CheckMachine(context.Background(), someDeviceID, srv.URL+"/v2/?id="+srv.id(), time.Now)
	if got.State != StatusWaiting || got.Age < 29*time.Second || got.Age > 32*time.Second {
		t.Errorf("CheckMachine = %+v, want waiting about 30 s", got)
	}
	if srv.requests.Load() != 1 {
		t.Errorf("%d requests, want 1", srv.requests.Load())
	}

	// A server that is not who the URL says it is, and one that is tired of being asked.
	bad := CheckMachine(context.Background(), someDeviceID, srv.URL+"/v2/?id="+someDeviceID, time.Now)
	if bad.State != StatusNoNetwork {
		t.Errorf("wrong server: %+v, want NO_NETWORK", bad)
	}
	limited := newRateLimitingServer(t, "350")
	lim := CheckMachine(context.Background(), someDeviceID, limited.URL+"/v2/?insecure", time.Now)
	if lim.State != StatusRateLimited || lim.RetryAfter != 350*time.Second {
		t.Errorf("429: %+v, want RATE_LIMITED 350s", lim)
	}
}

// A discovery server that answers every lookup with HTTP 429 and the given Retry-After.
func newRateLimitingServer(t *testing.T, retryAfter string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", retryAfter)
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	return srv
}
