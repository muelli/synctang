// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// discoveryServer is a TLS discovery server with a self-signed certificate,
// which no system root vouches for, answering every lookup for want with the
// given record. It counts the requests that reach its handler and keeps the
// last query, so a test can say both "never asked" and "asked like this".
type discoveryServer struct {
	*httptest.Server
	requests  atomic.Int32
	lastQuery atomic.Value // url.Values
}

func newDiscoveryServer(t *testing.T, seen string, addresses ...string) *discoveryServer {
	t.Helper()
	d := &discoveryServer{}
	d.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.requests.Add(1)
		d.lastQuery.Store(r.URL.Query())
		if len(addresses) == 0 {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"seen": seen, "addresses": addresses})
	}))
	t.Cleanup(d.Close)
	return d
}

// id is the server's Device ID, as a Syncthing discovery URL's ?id= names it.
func (d *discoveryServer) id() string {
	return protocol.NewDeviceID(d.Certificate().Raw).String()
}

const someDeviceID = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"

func TestLookupPinnedByDeviceIDAcceptsASelfSignedServer(t *testing.T) {
	seen := "2026-11-03T10:00:00Z"
	srv := newDiscoveryServer(t, seen, "relay://192.0.2.1:22067/?id="+someDeviceID)

	rec, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?id="+srv.id())
	if err != nil {
		t.Fatalf("lookupRecord: %v", err)
	}
	if rec == nil || len(rec.Addresses) != 1 || !strings.HasPrefix(rec.Addresses[0], "relay://192.0.2.1:22067") {
		t.Fatalf("record = %+v", rec)
	}
	want, _ := time.Parse(time.RFC3339, seen)
	if !rec.Seen.Equal(want) {
		t.Errorf("Seen = %v, want %v: the timestamp is what tells a key holder the machine is recent", rec.Seen, want)
	}
}

func TestLookupPinnedToTheWrongDeviceIDIsRefusedBeforeAnythingIsSent(t *testing.T) {
	srv := newDiscoveryServer(t, "2026-11-03T10:00:00Z", "tcp://192.0.2.1:22000")

	_, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?id="+someDeviceID)
	if err == nil {
		t.Fatal("a server whose certificate is not the pinned Device ID was accepted")
	}
	for _, want := range []string{"id mismatch", srv.id(), someDeviceID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if n := srv.requests.Load(); n != 0 {
		t.Errorf("%d request(s) reached the wrong server; the check must come before the request", n)
	}
}

func TestLookupInsecureAcceptsASelfSignedServer(t *testing.T) {
	srv := newDiscoveryServer(t, "", "tcp://192.0.2.1:22000")
	for _, q := range []string{"?insecure", "?insecure=true"} {
		rec, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/"+q)
		if err != nil || rec == nil || len(rec.Addresses) != 1 {
			t.Errorf("%s: %+v, %v", q, rec, err)
		}
	}
	if _, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?insecure=false"); err == nil {
		t.Error("?insecure=false must mean verify, like Syncthing's own")
	}
}

// Neither id nor insecure means the system's CAs, exactly as before, and a
// self-signed server is not among them.
func TestLookupWithNeitherUsesTheSystemCAs(t *testing.T) {
	srv := newDiscoveryServer(t, "", "tcp://192.0.2.1:22000")
	_, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/")
	if err == nil {
		t.Fatal("a self-signed server was accepted with no id and no insecure")
	}
	if srv.requests.Load() != 0 {
		t.Error("a request reached a server whose certificate was not trusted")
	}
}

// The server would not understand id and insecure, and Syncthing strips them;
// anything else in the query is the owner's and is kept.
func TestLookupSendsTheDeviceAndNotTheTrustOptions(t *testing.T) {
	srv := newDiscoveryServer(t, "", "tcp://192.0.2.1:22000")
	if _, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?x=1&id="+srv.id()+"&insecure"); err != nil {
		t.Fatal(err)
	}
	q := srv.lastQuery.Load().(url.Values)
	if q.Get("device") != someDeviceID || q.Get("x") != "1" {
		t.Errorf("query = %v", q)
	}
	if q.Has("id") || q.Has("insecure") {
		t.Errorf("trust options were sent to the server: %v", q)
	}
}

func TestLookupNotFoundIsNoRecordNotAnError(t *testing.T) {
	srv := newDiscoveryServer(t, "") // no addresses: answers 404
	rec, err := lookupRecord(context.Background(), someDeviceID, srv.URL+"/v2/?insecure")
	if rec != nil || err != nil {
		t.Errorf("got %+v, %v", rec, err)
	}
}

// Through Dial, so that the check is shown to be what stands between a key
// holder and the machine, not just a function that exists.
func TestDialRefusesADiscoveryServerWithTheWrongID(t *testing.T) {
	srv := newDiscoveryServer(t, "", "tcp://127.0.0.1:1")
	tr := &SyncthingRelay{Cert: testCert(t), DiscoveryURL: srv.URL + "/v2/?id=" + someDeviceID}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err := tr.Dial(ctx, DialOptions{PeerID: someDeviceID})
	if err == nil || !strings.Contains(err.Error(), "id mismatch") {
		t.Fatalf("Dial: %v, want a refusal naming the id mismatch", err)
	}
	if srv.requests.Load() != 0 {
		t.Error("Dial asked a server whose identity it could not confirm")
	}
}

func TestAnnounceRefusesPlainHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL + "/v2/"}}
	err := relay.announceOnce(context.Background(), []string{"tcp://192.0.2.1:22000"})
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("announceOnce: %v, want a refusal that says plain http", err)
	}
	if hits.Load() != 0 {
		t.Error("an announcement went out over plain http")
	}
}

// id and insecure are for the client; the announce endpoint gets neither.
func TestAnnounceDoesNotSendTheTrustOptions(t *testing.T) {
	var q atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q.Store(r.URL.Query())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	id := protocol.NewDeviceID(srv.Certificate().Raw).String()
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL + "/v2/?x=1&id=" + id + "&insecure"}}
	if err := relay.announceOnce(context.Background(), []string{"tcp://192.0.2.1:22000"}); err != nil {
		t.Fatal(err)
	}
	got := q.Load().(url.Values)
	if got.Has("id") || got.Has("insecure") || got.Get("x") != "1" {
		t.Errorf("announce query = %v", got)
	}
}

// A machine whose discovery URL says ?id= announces only to the server with
// that Device ID, as it looks up only on it: otherwise an announcement, which
// says where the machine is, could go to whoever answers at that address.
func TestAnnouncePinnedByDeviceIDPostsToTheRightServer(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	id := protocol.NewDeviceID(srv.Certificate().Raw).String()

	// No httpClient override: the pin alone has to be what accepts a
	// self-signed server.
	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL + "/v2/?id=" + id}}
	if err := relay.announceOnce(context.Background(), []string{"tcp://192.0.2.1:22000"}); err != nil {
		t.Fatalf("announceOnce: %v", err)
	}
	if posts.Load() != 1 {
		t.Errorf("%d announcements arrived, want 1", posts.Load())
	}
}

func TestAnnouncePinnedToTheWrongDeviceIDPostsNothing(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL + "/v2/?id=" + someDeviceID}}
	// Even a client that would trust this server must not be used for a pinned URL.
	relay.httpClient = srv.Client()
	err := relay.announceOnce(context.Background(), []string{"tcp://192.0.2.1:22000"})
	if err == nil || !strings.Contains(err.Error(), "id mismatch") {
		t.Fatalf("announceOnce: %v, want a refusal naming the id mismatch", err)
	}
	if posts.Load() != 0 {
		t.Error("an announcement reached a server that is not the pinned one")
	}
}

// Without an id the announcement is made exactly as before: to whatever the
// certificate is, which the existing tests cover; here with ?insecure, which
// must not turn into a pin.
func TestAnnounceInsecureAndPlainStayUnpinned(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	for _, q := range []string{"", "?insecure"} {
		relay := &SyncthingRelay{Cert: testCert(t), AnnounceURLs: []string{srv.URL + "/v2/" + q}}
		if err := relay.announceOnce(context.Background(), []string{"tcp://192.0.2.1:22000"}); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	if posts.Load() != 2 {
		t.Errorf("%d announcements arrived, want 2", posts.Load())
	}
}
