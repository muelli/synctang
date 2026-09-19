// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	cert, err := LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatalf("LoadOrCreateCert: %v", err)
	}
	return cert
}

// Syncthing global discovery uses different endpoints for announcing
// (an authenticated POST identifying the announcer by its TLS client
// certificate) and looking a peer up (an unauthenticated GET); they
// are not the same URL under different HTTP methods. Found running
// this against a real deployment: SyncthingRelay was POSTing
// announcements to DiscoveryURL, the lookup endpoint, so nothing it
// announced ever became visible to a lookup against the correct
// endpoint, silently, forever (announceLoop discards announceOnce's
// error).
func TestAnnounceOnceUsesAnnounceURLsNotDiscoveryURL(t *testing.T) {
	var gotRequest bool
	announceSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequest = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer announceSrv.Close()

	relay := &SyncthingRelay{
		Cert:         testCert(t),
		DiscoveryURL: "https://127.0.0.1:1/unreachable-lookup-url", // must never be hit
		AnnounceURLs: []string{announceSrv.URL},
	}
	relay.httpClient = announceSrv.Client()

	if err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"}); err != nil {
		t.Fatalf("announceOnce: %v", err)
	}
	if !gotRequest {
		t.Fatal("announce server never received a request; announceOnce used the wrong URL")
	}
}

func TestAnnounceOnceSucceedsIfAnyURLWorks(t *testing.T) {
	goodSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer goodSrv.Close()

	badSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	badSrv.Close() // closed: every request to it fails to connect at all

	relay := &SyncthingRelay{
		Cert:         testCert(t),
		AnnounceURLs: []string{badSrv.URL, goodSrv.URL},
	}
	relay.httpClient = goodSrv.Client()

	if err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"}); err != nil {
		t.Fatalf("announceOnce: %v, want success since one of two URLs works", err)
	}
}

func TestAnnounceOnceFailsIfEveryURLFails(t *testing.T) {
	badSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	relay := &SyncthingRelay{
		Cert:         testCert(t),
		AnnounceURLs: []string{badSrv.URL},
	}
	relay.httpClient = badSrv.Client()

	if err := relay.announceOnce(context.Background(), []string{"tcp://127.0.0.1:22000"}); err == nil {
		t.Fatal("expected an error when every announce URL fails, got nil")
	}
}

func TestAnnounceOnceSendsAddresses(t *testing.T) {
	var gotBody struct {
		Addresses []string `json:"addresses"`
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding announce body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	relay := &SyncthingRelay{
		Cert:         testCert(t),
		AnnounceURLs: []string{srv.URL},
	}
	relay.httpClient = srv.Client()

	want := []string{"tcp://127.0.0.1:22000"}
	if err := relay.announceOnce(context.Background(), want); err != nil {
		t.Fatalf("announceOnce: %v", err)
	}
	if len(gotBody.Addresses) != 1 || gotBody.Addresses[0] != want[0] {
		t.Fatalf("announce body addresses: got %v want %v", gotBody.Addresses, want)
	}
}

// recordingHandler is a minimal slog.Handler that just remembers
// every record's message, for tests to assert on without needing a
// real log sink.
type recordingHandler struct {
	messages *[]string
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.messages = append(*h.messages, r.Message)
	return nil
}
func (h recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(_ string) slog.Handler      { return h }

// A caller-supplied Logger, not a hardcoded log.Printf, is what makes
// SyncthingRelay usable from a context (Android's gomobile bind, or a
// plain "go test") that has no systemd journal to write structured
// entries to at all.
func TestAnnounceLoopLogsThroughCustomLogger(t *testing.T) {
	badSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	var messages []string
	relay := &SyncthingRelay{
		Cert:             testCert(t),
		AnnounceURLs:     []string{badSrv.URL},
		AnnounceInterval: 5 * time.Millisecond,
		Logger:           slog.New(recordingHandler{messages: &messages}),
	}
	relay.httpClient = badSrv.Client()

	stop := relay.announceLoop(context.Background(), func() []string {
		return []string{"tcp://127.0.0.1:22000"}
	})
	time.Sleep(30 * time.Millisecond)
	stop()

	if len(messages) == 0 {
		t.Fatal("expected the failing announce to be logged through the custom Logger, got nothing")
	}
}

// A machine waiting at its LUKS prompt announces its current relay
// address every announceInterval. The relay client it asks for that
// address returns nil whenever it is not currently connected to a
// relay, and announceOnce treats "no addresses" as nothing to do and
// returns success. Together that means a machine which has lost its
// relay announces nothing, reports nothing, and looks exactly like one
// that is announcing fine, until whoever comes to unlock it finds it
// unreachable. Observed for real: a test VM announced once, then went
// silent for nine minutes with no log line of any kind while its
// discovery record went stale and the relay forgot it.
func TestAnnounceLoopWarnsWhenThereIsNothingToAnnounce(t *testing.T) {
	var messages []string
	relay := &SyncthingRelay{
		Cert:             testCert(t),
		AnnounceInterval: 5 * time.Millisecond,
		Logger:           slog.New(recordingHandler{messages: &messages}),
	}

	stop := relay.announceLoop(context.Background(), func() []string { return nil })
	time.Sleep(30 * time.Millisecond)
	stop()

	if len(messages) == 0 {
		t.Fatal("a loop that announced nothing at all logged nothing at all")
	}
}

// watchRelayLoss is what stops Listen waiting forever on a relay client
// that has given up. The upstream client never closes its invitations
// channel, not even when its Serve has returned "could not find a
// connectable relay", so the obvious "case inv, ok := <-Invitations()"
// cannot detect this: ok is never false. Without a watchdog the agent
// blocks on that channel for the rest of the boot.
func TestWatchRelayLossReportsARelayThatNeverComesBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := watchRelayLoss(ctx, func() *url.URL { return nil }, func() error { return nil },
		30*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for a relay client that never has a URI")
	}
	if ctx.Err() != nil {
		t.Fatal("watchRelayLoss waited for ctx rather than reporting the loss itself")
	}
}

// Relays rotate: the dynamic client disconnects from one and connects
// to the next, and its URI is nil in between. That gap is normal and
// must not fail a listen that is about to keep working.
func TestWatchRelayLossToleratesABriefGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	live, _ := url.Parse("relay://192.0.2.1:22067")
	err := watchRelayLoss(ctx, func() *url.URL {
		if time.Since(start) < 30*time.Millisecond {
			return nil
		}
		return live
	}, func() error { return nil }, 80*time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("a 30ms gap under an 80ms grace should not be reported as a loss: %v", err)
	}
}

// A relay client whose Serve has already returned is done, and waiting
// out the grace period for it changes nothing.
func TestWatchRelayLossReportsAStoppedClientImmediately(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	live, _ := url.Parse("relay://192.0.2.1:22067")
	start := time.Now()
	err := watchRelayLoss(ctx, func() *url.URL { return live },
		func() error { return errors.New("could not find a connectable relay") },
		time.Hour, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for a relay client whose Serve has returned")
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited for the grace period instead of reporting the stopped client at once")
	}
}
