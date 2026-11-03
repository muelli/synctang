// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// A discovery server with a self-signed certificate, answering a lookup with the record
// built by record (nil means 404), and counting what it is asked.
type statusServer struct {
	*httptest.Server
	requests atomic.Int32
}

func newStatusServer(t *testing.T, record func() map[string]any) *statusServer {
	t.Helper()
	s := &statusServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		rec := record()
		if rec == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rec)
	}))
	t.Cleanup(s.Close)
	return s
}

// pinned is the server's URL as a phone is given it: trusted by its Device ID.
func (s *statusServer) pinned() string {
	return s.URL + "/v2/?id=" + protocol.NewDeviceID(s.Certificate().Raw).String()
}

const statusMachine = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"

func seenAgo(d time.Duration) func() map[string]any {
	return func() map[string]any {
		return map[string]any{"seen": time.Now().Add(-d).UTC().Format(time.RFC3339), "addresses": []string{"relay://192.0.2.1:22067/?id=X"}}
	}
}

func TestMachineStatusStates(t *testing.T) {
	for _, tt := range []struct {
		name   string
		record func() map[string]any
		state  string
		minAge int
		maxAge int
	}{
		{"announced seconds ago", seenAgo(10 * time.Second), StatusWaiting, 9, 12},
		{"announced right at the limit", seenAgo(149 * time.Second), StatusWaiting, 148, 151},
		{"announced a little too long ago", seenAgo(151 * time.Second), StatusStale, 150, 153},
		{"announced an hour ago", seenAgo(time.Hour), StatusStale, 3598, 3603},
		{"a timestamp in the future counts as now", seenAgo(-2 * time.Hour), StatusWaiting, 0, 0},
		{"a record with no timestamp", func() map[string]any { return map[string]any{"addresses": []string{"tcp://192.0.2.1:22000"}} }, StatusStale, -1, -1},
		{"a record with a timestamp nobody can read", func() map[string]any {
			return map[string]any{"seen": "yesterday-ish", "addresses": []string{"tcp://192.0.2.1:22000"}}
		}, StatusStale, -1, -1},
		{"no record at all", func() map[string]any { return nil }, StatusAbsent, -1, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newStatusServer(t, tt.record)
			got, err := CheckMachineStatus(statusMachine, srv.pinned())
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tt.state {
				t.Errorf("state %q, want %q (age %d)", got.State, tt.state, got.AgeSeconds)
			}
			if got.AgeSeconds < tt.minAge || got.AgeSeconds > tt.maxAge {
				t.Errorf("age %d, want %d to %d", got.AgeSeconds, tt.minAge, tt.maxAge)
			}
		})
	}
}

// One lookup, and nothing else: the machine serves one session and a probe would use it up.
func TestMachineStatusAsksDiscoveryOnceAndNothingElse(t *testing.T) {
	srv := newStatusServer(t, seenAgo(time.Second))
	if _, err := CheckMachineStatus(statusMachine, srv.pinned()); err != nil {
		t.Fatal(err)
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("%d requests to the discovery server, want exactly one", n)
	}
}

// The lookup itself failing is "cannot check", not "absent": the person is told the
// phone cannot reach the discovery server, not that the machine is off.
func TestMachineStatusSaysNoNetworkWhenTheLookupFails(t *testing.T) {
	srv := newStatusServer(t, seenAgo(time.Second))
	url := srv.pinned()
	srv.Close() // nothing is listening any more
	got, err := CheckMachineStatus(statusMachine, url)
	if err != nil || got.State != StatusNoNetwork || got.AgeSeconds != -1 {
		t.Errorf("%+v, %v", got, err)
	}

	// A server that is not the one the phone was told to trust is a failed lookup too.
	other := newStatusServer(t, seenAgo(time.Second))
	wrong := other.URL + "/v2/?id=" + statusMachine
	got, err = CheckMachineStatus(statusMachine, wrong)
	if err != nil || got.State != StatusNoNetwork {
		t.Errorf("wrong server identity: %+v, %v", got, err)
	}
	if other.requests.Load() != 0 {
		t.Error("a request reached a server that was not the pinned one")
	}
}

func TestMachineStatusRefusesInputThatCannotBeChecked(t *testing.T) {
	if _, err := CheckMachineStatus("", ""); err == nil {
		t.Error("an empty device id was accepted")
	}
	if _, err := CheckMachineStatus(statusMachine, "ftp://x.example.net/"); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("a bad discovery URL: %v", err)
	}
	if _, err := CheckMachineStatus("not a device id", ""); err == nil {
		t.Error("a malformed device id was accepted")
	}
}

// A discovery server that is tired of being asked: HTTP 429 with the given Retry-After
// header (none when empty).
func newLimitingServer(t *testing.T, retryAfter string) *statusServer {
	t.Helper()
	s := &statusServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestMachineStatusSaysRateLimitedNotNoNetworkFor429(t *testing.T) {
	soon := time.Now().Add(200 * time.Second).UTC().Format(http.TimeFormat)
	for _, tt := range []struct {
		name, header string
		min, max     int
	}{
		{"seconds as told", "350", 350, 350},
		{"the longest the public server was seen to ask", "1200", 600, 600},
		{"less than the floor", "5", 30, 30},
		{"zero", "0", 30, 30},
		{"an HTTP date", soon, 198, 201},
		{"garbage is a minute", "later", 60, 60},
		{"no header is a minute", "", 60, 60},
		{"more than the cap", "86400", 600, 600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newLimitingServer(t, tt.header)
			got, err := CheckMachineStatus(statusMachine, srv.pinned())
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StatusRateLimited {
				t.Fatalf("state %q, want %q", got.State, StatusRateLimited)
			}
			if got.RetryAfterSeconds < tt.min || got.RetryAfterSeconds > tt.max {
				t.Errorf("RetryAfterSeconds %d, want %d to %d", got.RetryAfterSeconds, tt.min, tt.max)
			}
			if srv.requests.Load() != 1 {
				t.Errorf("%d requests, want 1: a 429 is not retried here", srv.requests.Load())
			}
		})
	}
}

func TestHonouredRetryAfter(t *testing.T) {
	for in, want := range map[time.Duration]int{
		-time.Second: 30, 0: 30, 29 * time.Second: 30, 30 * time.Second: 30, 31 * time.Second: 31,
		600 * time.Second: 600, 601 * time.Second: 600, 24 * time.Hour: 600,
		1500 * time.Millisecond: 30, 90*time.Second + 400*time.Millisecond: 91,
	} {
		if got := honouredRetryAfter(in); got != want {
			t.Errorf("honouredRetryAfter(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestOtherStatusesCarryNoRetryAfter(t *testing.T) {
	srv := newStatusServer(t, seenAgo(time.Second))
	got, _ := CheckMachineStatus(statusMachine, srv.pinned())
	if got.RetryAfterSeconds != 0 {
		t.Errorf("RetryAfterSeconds %d on a %s answer, want 0", got.RetryAfterSeconds, got.State)
	}
}
