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

// A machine that is waiting is one whose record is young. The phone calls a record younger
// than WaitingRecordMaxAge "waiting now", so the machine has to publish again well within
// that, and more than once in it, so that a single lost announcement does not turn a waiting
// machine into a stale one.
func TestTheDefaultRefreshKeepsAWaitingRecordYoung(t *testing.T) {
	if defaultAnnounceRefresh != time.Minute {
		t.Errorf("defaultAnnounceRefresh = %s, want a minute", defaultAnnounceRefresh)
	}
	// The record is as old as the time since the last refresh, and a lookup sees it some seconds after it was made:
	// the first pace has to leave room for that inside the time a record counts as waiting.
	if WaitingRecordMaxAge-defaultAnnounceRefresh < 30*time.Second {
		t.Errorf("a refresh every %s leaves less than 30 s of the %s a record counts as waiting", defaultAnnounceRefresh, WaitingRecordMaxAge)
	}
	if WaitingRecordMaxAge != 90*time.Second {
		t.Errorf("WaitingRecordMaxAge = %s, want the 90 s the phone's dot is green for", WaitingRecordMaxAge)
	}
	if got := (&SyncthingRelay{}).announceRefresh(); got != defaultAnnounceRefresh {
		t.Errorf("an unset AnnounceRefresh means %s, want the default %s", got, defaultAnnounceRefresh)
	}
}

// Over a shortened interval, the loop announces an unchanged address again and again, not once.
func TestTheAnnounceLoopRepublishesAnUnchangedAddressAtTheRefreshInterval(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	relay := &SyncthingRelay{
		Cert:             testCert(t),
		AnnounceURLs:     []string{srv.URL},
		AnnounceInterval: 20 * time.Millisecond,
		AnnounceRefresh:  200 * time.Millisecond,
	}
	relay.httpClient = srv.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := relay.announceLoop(ctx, func() []string { return []string{"relay://192.0.2.1:22067/?id=X"} })
	time.Sleep(1100 * time.Millisecond)
	stop()
	// One at the start, then one for each 200 ms: about six, never one and never dozens.
	if n := posts.Load(); n < 4 || n > 8 {
		t.Errorf("%d announcements in 1.1 s at a 200 ms refresh, want about 6", n)
	}
}
