// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

const hintMachine = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"

// unlockAgainst runs "synctang-keyholder unlock" against a discovery server answering as
// answer says, with a short timeout, and returns what it printed and how often the server
// was asked.
func unlockAgainst(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (stderr string, code int, requests int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	id := protocol.NewDeviceID(srv.Certificate().Raw).String()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // no settings file of the person running the tests
	t.Setenv("HOME", dir)
	keyFile := filepath.Join(dir, "key")
	if _, err := generateAndSave(keyFile, false); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code = runUnlock([]string{
		"--key-file", keyFile, "--transport-key-file", filepath.Join(dir, "transport.pem"),
		"--timeout", "1500ms", "--yes",
		"--discovery", srv.URL + "/v2/?id=" + id, hintMachine,
	}, strings.NewReader(""), &out, &errOut)
	return errOut.String(), code, n.Load()
}

func TestUnlockThatCannotReachTheMachineSaysWhatDiscoveryKnows(t *testing.T) {
	record := func(seenAgo time.Duration) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			// A direct address nothing listens on: the dial fails, the record is what is read.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"seen":      time.Now().Add(-seenAgo).UTC().Format(time.RFC3339),
				"addresses": []string{"tcp://127.0.0.1:1"},
			})
		}
	}
	for _, tc := range []struct {
		name   string
		answer func(http.ResponseWriter, *http.Request)
		want   string
	}{
		{"announced a moment ago", record(20 * time.Second),
			"The machine announced itself 2"}, // 20 s ago, 21 or 22 by the time it is read
		{"announced long ago", record(47 * time.Minute),
			"The machine last announced itself 47 min ago, so it may not be at its prompt. Check the machine."},
		{"never announced", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
			"The machine has not announced itself. It may still be starting, switched off, or using another discovery server."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, code, requests := unlockAgainst(t, tc.answer)
			if code != 1 {
				t.Errorf("exit code %d, want 1; stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr lacks %q:\n%s", tc.want, stderr)
			}
			// The dial's own first lookup, and the one for this text. Not one more.
			if requests != 2 {
				t.Errorf("%d lookups, want 2 (the dial's first and one for the hint)", requests)
			}
		})
	}
}

func TestUnlockSaysNothingOfDiscoveryWhenTheServerIsRateLimiting(t *testing.T) {
	stderr, code, _ := unlockAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "350")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	for _, claim := range []string{"announced itself", "has not announced"} {
		if strings.Contains(stderr, claim) {
			t.Errorf("claimed %q although the server only said 429:\n%s", claim, stderr)
		}
	}
}

func TestUnlockOfADirectAddressDoesNotLookAnythingUp(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked.Add(1) }))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	keyFile := filepath.Join(dir, "key")
	if _, err := generateAndSave(keyFile, false); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runUnlock([]string{
		"--key-file", keyFile, "--transport-key-file", filepath.Join(dir, "transport.pem"),
		"--timeout", "500ms", "--direct-addr", "127.0.0.1:1",
		"--discovery", srv.URL + "/v2/?id=" + protocol.NewDeviceID(srv.Certificate().Raw).String(), hintMachine,
	}, strings.NewReader(""), &out, &errOut)
	if code != 1 || strings.Contains(errOut.String(), "announced") {
		t.Errorf("code %d, stderr:\n%s", code, errOut.String())
	}
	if asked.Load() != 0 {
		t.Errorf("discovery was asked %d times for a dial that names its address", asked.Load())
	}
}
