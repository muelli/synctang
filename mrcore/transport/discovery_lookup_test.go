// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestDiscoveryDefaults(t *testing.T) {
	if defaultDiscoveryURL != "https://discovery-lookup.syncthing.net/v2/" {
		t.Errorf("defaultDiscoveryURL = %q", defaultDiscoveryURL)
	}
	if defaultAnnounceRefresh != 30*time.Minute {
		t.Errorf("defaultAnnounceRefresh = %v, want 30m", defaultAnnounceRefresh)
	}
}

func TestLookupRecordParsesSeenAndAddresses(t *testing.T) {
	var gotQuery, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		w.Write([]byte(`{"seen":"2026-09-30T12:34:56Z","addresses":["tcp://10.0.0.1:22000","relay://r.example:22067/?id=X"]}`))
	}))
	defer srv.Close()

	rec, err := lookupRecord(context.Background(), "DEVICE-ID", srv.URL+"/v2/")
	if err != nil {
		t.Fatalf("lookupRecord: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotQuery != "device=DEVICE-ID" {
		t.Errorf("query = %q, want device=DEVICE-ID", gotQuery)
	}
	wantSeen := time.Date(2026, 9, 30, 12, 34, 56, 0, time.UTC)
	if rec == nil || !rec.Seen.Equal(wantSeen) {
		t.Fatalf("record = %+v, want Seen %v", rec, wantSeen)
	}
	want := []string{"tcp://10.0.0.1:22000", "relay://r.example:22067/?id=X"}
	if !reflect.DeepEqual(rec.Addresses, want) {
		t.Errorf("Addresses = %v, want %v", rec.Addresses, want)
	}
}

func TestLookupRecordAppendsToExistingQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := lookupRecord(context.Background(), "D1", srv.URL+"/?noid=1"); err != nil {
		t.Fatalf("lookupRecord: %v", err)
	}
	if gotQuery != "noid=1&device=D1" {
		t.Errorf("query = %q, want noid=1&device=D1", gotQuery)
	}
}

func TestLookupRecordNotFoundIsNilNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rec, err := lookupRecord(context.Background(), "D1", srv.URL+"/v2/")
	if err != nil || rec != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", rec, err)
	}
}

func TestLookupRecordOtherStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	rec, err := lookupRecord(context.Background(), "D1", srv.URL+"/v2/")
	if err == nil || rec != nil {
		t.Fatalf("got (%v, %v), want error", rec, err)
	}
	if want := "discovery lookup returned HTTP 500"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestLookupRecordUndatedRecordKeepsAddresses(t *testing.T) {
	for name, body := range map[string]string{
		"missing":     `{"addresses":["tcp://a:1"]}`,
		"unparseable": `{"seen":"yesterday","addresses":["tcp://a:1"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer srv.Close()

			rec, err := lookupRecord(context.Background(), "D1", srv.URL+"/v2/")
			if err != nil {
				t.Fatalf("lookupRecord: %v", err)
			}
			if !rec.Seen.IsZero() {
				t.Errorf("Seen = %v, want zero", rec.Seen)
			}
			if !reflect.DeepEqual(rec.Addresses, []string{"tcp://a:1"}) {
				t.Errorf("Addresses = %v", rec.Addresses)
			}
		})
	}
}

func TestLookupRecordMalformedJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"seen":`))
	}))
	defer srv.Close()

	if rec, err := lookupRecord(context.Background(), "D1", srv.URL+"/v2/"); err == nil {
		t.Fatalf("got record %+v, want error", rec)
	}
}

func TestLookupRecordHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := lookupRecord(ctx, "D1", srv.URL+"/v2/"); err == nil {
		t.Fatal("want error from cancelled context")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v, context not honoured", time.Since(start))
	}
}

// The public endpoint must be verified: an httptest TLS server with a
// self-signed certificate has to be rejected, unlike the unlock-path
// lookups elsewhere.
func TestLookupRecordVerifiesServerCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := lookupRecord(context.Background(), "D1", srv.URL+"/v2/"); err == nil {
		t.Fatal("self-signed discovery server certificate was accepted")
	}
}
