// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A machine that has been at its prompt for a long time announces less and less often: a person who has just
// started it may look any minute, one who has not by now is not likely to this minute. The public discovery
// servers are shared, and syncthing-socket's own default is every thirty minutes.

func TestTheRefreshIntervalDoublesEveryDecayStepUpToTheCap(t *testing.T) {
	relay := &SyncthingRelay{AnnounceRefresh: time.Minute, AnnounceDecayStep: 10 * time.Minute, AnnounceRefreshMax: 30 * time.Minute}
	for _, tc := range []struct {
		waited time.Duration
		want   time.Duration
	}{
		{0, time.Minute},
		{9 * time.Minute, time.Minute},
		{10 * time.Minute, 2 * time.Minute},
		{25 * time.Minute, 4 * time.Minute},
		{35 * time.Minute, 8 * time.Minute},
		{45 * time.Minute, 16 * time.Minute},
		{55 * time.Minute, 30 * time.Minute},
		{24 * time.Hour, 30 * time.Minute},
	} {
		if got := relay.refreshAfter(tc.waited); got != tc.want {
			t.Errorf("after waiting %s: refresh every %s, want %s", tc.waited, got, tc.want)
		}
	}
}

func TestTheDefaultsAreAMinuteForTenMinutesAndThenLessAndLessUpToHalfAnHour(t *testing.T) {
	relay := &SyncthingRelay{}
	if relay.refreshAfter(0) != time.Minute || relay.refreshAfter(9*time.Minute) != time.Minute {
		t.Errorf("the first ten minutes: %s", relay.refreshAfter(0))
	}
	if relay.refreshAfter(10*time.Minute) != 2*time.Minute {
		t.Errorf("then every two minutes: %s", relay.refreshAfter(10*time.Minute))
	}
	if relay.refreshAfter(10*time.Hour) != 30*time.Minute {
		t.Errorf("in the end every half hour, as syncthing-socket does: %s", relay.refreshAfter(10*time.Hour))
	}
}

func TestAServerThatSaysHowOftenItWantsToBeToldCapsTheRefresh(t *testing.T) {
	relay := &SyncthingRelay{AnnounceRefresh: time.Minute, AnnounceDecayStep: time.Minute, AnnounceRefreshMax: time.Hour}
	relay.setServerReannounce(7 * time.Minute)
	if got := relay.refreshAfter(24 * time.Hour); got != 7*time.Minute {
		t.Errorf("the server asked to be told every 7 minutes: %s", got)
	}
	// Never more often than it was already going to.
	relay.setServerReannounce(10 * time.Second)
	if got := relay.refreshAfter(0); got != time.Minute {
		t.Errorf("a cap below the first interval must not speed it up: %s", got)
	}
}

func TestTheAnnounceLoopSlowsDownAndSpeedsUpAgainWhenTheAddressChanges(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	relay := &SyncthingRelay{
		Cert: testCert(t), AnnounceURLs: []string{srv.URL}, AnnounceInterval: 5 * time.Millisecond,
		AnnounceRefresh: 20 * time.Millisecond, AnnounceDecayStep: 100 * time.Millisecond, AnnounceRefreshMax: 160 * time.Millisecond,
	}
	relay.httpClient = srv.Client()
	var addr atomic.Value
	addr.Store("relay://192.0.2.1:22067")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := relay.announceLoop(ctx, func() []string { return []string{addr.Load().(string)} })
	time.Sleep(300 * time.Millisecond)
	early := posts.Load() // 20 ms for 100 ms (5), 40 ms for 100 ms (2-3), 80 ms after: about 9
	time.Sleep(500 * time.Millisecond)
	late := posts.Load() - early // at the 160 ms cap: about 3
	if early < 6 {
		t.Errorf("only %d announcements in the first 300 ms at a 20 ms start", early)
	}
	if late > 5 {
		t.Errorf("%d announcements in the next 500 ms, which should be at the 160 ms cap (about 3)", late)
	}

	// A new address is news: the pace starts again.
	addr.Store("relay://192.0.2.99:22067")
	before := posts.Load()
	time.Sleep(250 * time.Millisecond)
	stop()
	if got := posts.Load() - before; got < 5 {
		t.Errorf("%d announcements in 250 ms after the address changed, the quick pace should be back (about 8)", got)
	}
}

func TestTheReannounceAfterHeaderOfTheServerIsRead(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Reannounce-After", "420")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL}, AnnounceRefresh: time.Minute, AnnounceDecayStep: time.Minute, AnnounceRefreshMax: time.Hour}
	relay.httpClient = srv.Client()
	if err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"}); err != nil {
		t.Fatal(err)
	}
	if got := relay.refreshAfter(24 * time.Hour); got != 7*time.Minute {
		t.Errorf("Reannounce-After: 420 gave a cap of %s", got)
	}
}
