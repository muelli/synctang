// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testToken(t *testing.T) Token {
	t.Helper()
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	_, rec, err := Enrol(g, S)
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}
	rec.Transport = json.RawMessage(`{"kind":"syncthing-relay","device_id":"ABCD1234"}`)

	return Token{
		Version:    1,
		Recipients: []Recipient{rec},
		Machine: MachineInfo{
			Name:        "test-machine",
			TransportID: "MACHINE-DEVICE-ID",
		},
	}
}

// T1.4: token encode/decode round-trip, matching field for field.
func TestTokenRoundTrip(t *testing.T) {
	want := testToken(t)

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Token
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Version != want.Version {
		t.Fatalf("version: got %d want %d", got.Version, want.Version)
	}
	if got.Machine != want.Machine {
		t.Fatalf("machine: got %+v want %+v", got.Machine, want.Machine)
	}
	if len(got.Recipients) != len(want.Recipients) {
		t.Fatalf("recipients: got %d want %d", len(got.Recipients), len(want.Recipients))
	}
	gr, wr := got.Recipients[0], want.Recipients[0]
	if !bytes.Equal(gr.Kid, wr.Kid) || !bytes.Equal(gr.S, wr.S) || !bytes.Equal(gr.C, wr.C) ||
		!bytes.Equal(gr.Ciphertext, wr.Ciphertext) || !bytes.Equal(gr.Nonce, wr.Nonce) {
		t.Fatalf("recipient crypto fields did not round-trip:\n got=%+v\n want=%+v", gr, wr)
	}
	if !bytes.Equal(gr.Transport, wr.Transport) {
		t.Fatalf("transport: got %s want %s", gr.Transport, wr.Transport)
	}
}

// T1.4: fields this version of mrcore does not know about must survive
// a decode/encode cycle unchanged, so a newer machine's token does not
// lose data when handled by an older mrcore, or vice versa.
func TestTokenPreservesUnknownFields(t *testing.T) {
	const input = `{
		"type": "mr-1",
		"version": 1,
		"recipients": [],
		"machine": {"name": "m", "transport_id": "t"},
		"future_field": {"nested": "value", "n": 7}
	}`

	var tok Token
	if err := json.Unmarshal([]byte(input), &tok); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	out, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !strings.Contains(string(out), `"future_field"`) {
		t.Fatalf("unknown field was dropped: %s", out)
	}
	if !strings.Contains(string(out), `"nested":"value"`) {
		t.Fatalf("unknown field's contents were dropped: %s", out)
	}
}

// T1.4: an unsupported version must be rejected explicitly, not
// silently misinterpreted.
func TestTokenRejectsUnsupportedVersion(t *testing.T) {
	const input = `{"type": "mr-1", "version": 99, "recipients": [], "machine": {"name":"m","transport_id":"t"}}`

	var tok Token
	err := json.Unmarshal([]byte(input), &tok)
	if err == nil {
		t.Fatal("expected an error decoding an unsupported version, got nil")
	}
}

// T1.4: a wrong protocol type must be rejected explicitly.
func TestTokenRejectsWrongType(t *testing.T) {
	const input = `{"type": "not-mr-1", "version": 1, "recipients": [], "machine": {"name":"m","transport_id":"t"}}`

	var tok Token
	err := json.Unmarshal([]byte(input), &tok)
	if err == nil {
		t.Fatal("expected an error decoding a token with the wrong type, got nil")
	}
}
