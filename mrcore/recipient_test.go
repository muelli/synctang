// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"encoding/json"
	"testing"
)

func TestRecipientTransportDeviceIDEmpty(t *testing.T) {
	var r Recipient
	if id, ok := r.TransportDeviceID(); ok || id != "" {
		t.Fatalf("TransportDeviceID on a Recipient with no Transport = (%q, %v), want (\"\", false)", id, ok)
	}
}

func TestRecipientTransportDeviceIDPresent(t *testing.T) {
	r := Recipient{Transport: json.RawMessage(`{"device_id":"ABCD-1234"}`)}
	id, ok := r.TransportDeviceID()
	if !ok || id != "ABCD-1234" {
		t.Fatalf("TransportDeviceID = (%q, %v), want (\"ABCD-1234\", true)", id, ok)
	}
}

func TestRecipientTransportDeviceIDMissingKey(t *testing.T) {
	r := Recipient{Transport: json.RawMessage(`{"something_else":"x"}`)}
	if id, ok := r.TransportDeviceID(); ok || id != "" {
		t.Fatalf("TransportDeviceID with no device_id key = (%q, %v), want (\"\", false)", id, ok)
	}
}

func TestRecipientTransportDeviceIDGarbage(t *testing.T) {
	r := Recipient{Transport: json.RawMessage(`not json`)}
	if id, ok := r.TransportDeviceID(); ok || id != "" {
		t.Fatalf("TransportDeviceID on unparseable Transport = (%q, %v), want (\"\", false)", id, ok)
	}
}
