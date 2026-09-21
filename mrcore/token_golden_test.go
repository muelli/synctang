// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"encoding/json"
	"strings"
	"testing"
)

// A token is on-disk format: it lives in a LUKS2 header belonging to a
// volume somebody depends on, and cryptsetup will hand back exactly
// what was written. So the encoding is pinned byte for byte here,
// before the marshalling code is touched, and the same test is run
// afterwards. A refactor that changes the bytes is not a refactor.
//
// Deterministic because encoding/json sorts map keys, which is what
// MarshalJSON builds.
const goldenToken = `{"extra_from_a_newer_build":{"nested":[1,2]},"keyslots":["1","2"],` +
	`"machine":{"name":"study desktop","transport_id":"MACHINE-ID"},` +
	`"recipients":[{"kid":"AAEC","S":"BAUG","C":"BwgJ","ct":"CgsM","nonce":"DQ4P",` +
	`"transport":{"device_id":"KEYHOLDER-ID"}}],"type":"mr-1","unknown_scalar":42,"version":1}`

func goldenTokenValue(t *testing.T) Token {
	t.Helper()
	var tok Token
	if err := json.Unmarshal([]byte(goldenToken), &tok); err != nil {
		t.Fatalf("decoding the golden token: %v", err)
	}
	return tok
}

// What comes out must be exactly what went in, including the fields
// this build does not understand: a newer version of this project may
// have written them, and dropping them on a round trip would quietly
// corrupt somebody else's token.
func TestTokenEncodingIsByteForByteStable(t *testing.T) {
	tok := goldenTokenValue(t)

	got, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != goldenToken {
		t.Errorf("token encoding changed\n got: %s\nwant: %s", got, goldenToken)
	}
}

// Decoding is checked field by field rather than only as bytes, so a
// change that happened to round-trip while losing meaning is caught.
func TestTokenDecodesEveryField(t *testing.T) {
	tok := goldenTokenValue(t)

	if tok.Version != 1 {
		t.Errorf("Version = %d, want 1", tok.Version)
	}
	if len(tok.Keyslots) != 2 || tok.Keyslots[0] != "1" || tok.Keyslots[1] != "2" {
		t.Errorf("Keyslots = %v", tok.Keyslots)
	}
	if tok.Machine.Name != "study desktop" || tok.Machine.TransportID != "MACHINE-ID" {
		t.Errorf("Machine = %+v", tok.Machine)
	}
	if len(tok.Recipients) != 1 {
		t.Fatalf("Recipients = %d, want 1", len(tok.Recipients))
	}
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != "KEYHOLDER-ID" {
		t.Errorf("recipient transport id = (%q, %v)", id, ok)
	}
	if len(tok.extra) != 2 {
		t.Errorf("extra = %v, want the two fields this build does not know", tok.extra)
	}
}

// LUKS2 rejects a token with no keyslots array at all, so a nil slice
// has to encode as [] and not as null. Found the hard way against real
// cryptsetup, which says only "Failed to import token from file."
func TestTokenAlwaysEncodesAKeyslotsArray(t *testing.T) {
	tok := Token{Version: currentVersion}
	got, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(back["keyslots"]) != "[]" {
		t.Errorf("keyslots encoded as %s, want []", back["keyslots"])
	}
}

// Decoding must reject anything this build cannot honestly interpret,
// rather than guessing.
func TestTokenRejectsWrongTypeOrVersion(t *testing.T) {
	for name, doc := range map[string]string{
		"missing type":    `{"version":1,"keyslots":[]}`,
		"wrong type":      `{"type":"clevis","version":1,"keyslots":[]}`,
		"missing version": `{"type":"mr-1","keyslots":[]}`,
		"future version":  `{"type":"mr-1","version":2,"keyslots":[]}`,
		"not an object":   `["mr-1"]`,
	} {
		var tok Token
		if err := json.Unmarshal([]byte(doc), &tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A document belonging to another tool must be refused on its type,
// before this build tries to make sense of fields that are not its
// business. A LUKS2 header can carry tokens from several tools at
// once, so "this is not one of mine" has to come first.
func TestTokenRefusesForeignDocumentsOnTypeAlone(t *testing.T) {
	foreign := `{"type":"clevis","version":1,"recipients":"not even an array","jwe":{}}`
	var tok Token
	err := json.Unmarshal([]byte(foreign), &tok)
	if err == nil {
		t.Fatal("a clevis token was accepted")
	}
	if got := err.Error(); !strings.Contains(got, "unsupported type") {
		t.Errorf("rejected for the wrong reason: %v", got)
	}
}
