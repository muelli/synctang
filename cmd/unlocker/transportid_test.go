// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// A real, valid Syncthing Device ID in canonical form (the one the
// project's own docs use).
const canonicalTestID = "YIEAQU3-4EQIMH3-INVK5OV-QEPJZSW-GPKQF5R-UUGZZR2-KBIPPXD-BLGZ2AG"

func TestCanonicalDeviceIDAcceptsEveryShapeAHumanPastes(t *testing.T) {
	undashed := strings.ReplaceAll(canonicalTestID, "-", "")
	for _, in := range []string{
		canonicalTestID,
		strings.ToLower(canonicalTestID),
		undashed,
		strings.ToLower(undashed),
		"  " + canonicalTestID + "\n",
	} {
		got, err := canonicalDeviceID(in)
		if err != nil {
			t.Fatalf("canonicalDeviceID(%q): %v", in, err)
		}
		if got != canonicalTestID {
			t.Fatalf("canonicalDeviceID(%q) = %q, want %q", in, got, canonicalTestID)
		}
	}
}

func TestCanonicalDeviceIDRefusesNonsense(t *testing.T) {
	for _, in := range []string{"", "not an id", canonicalTestID[:20], canonicalTestID + "A", "\x01" + canonicalTestID} {
		if got, err := canonicalDeviceID(in); err == nil {
			t.Fatalf("canonicalDeviceID(%q) = %q, want an error", in, got)
		}
	}
}

// What goes into the token must be valid JSON whatever the input, and
// must round-trip through the accessor the agent uses.
func TestRecipientTransportBlobIsValidJSONAndRoundTrips(t *testing.T) {
	blob, err := recipientTransportBlob(strings.ToLower(canonicalTestID))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(blob) {
		t.Fatalf("not valid JSON: %s", blob)
	}
	got, ok := mrcore.Recipient{Transport: blob}.TransportDeviceID()
	if !ok || got != canonicalTestID {
		t.Fatalf("TransportDeviceID = (%q, %v), want %q", got, ok, canonicalTestID)
	}
}

func TestRecipientLookupAcceptsTheSameShapes(t *testing.T) {
	blob, err := recipientTransportBlob(canonicalTestID)
	if err != nil {
		t.Fatal(err)
	}
	tok := mrcore.Token{Recipients: []mrcore.Recipient{{Transport: blob}}}
	idx, err := recipientIndexByTransportID(tok, strings.ToLower(strings.ReplaceAll(canonicalTestID, "-", "")))
	if err != nil || idx != 0 {
		t.Fatalf("recipientIndexByTransportID = (%d, %v), want (0, nil)", idx, err)
	}
}
