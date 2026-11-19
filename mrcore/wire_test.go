// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteReadMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := RecoverRequest{Kid: []byte("some-kid"), X: []byte("some-point")}

	if err := WriteMessage(&buf, want); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var got RecoverRequest
	if err := ReadMessage(&buf, &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if string(got.Kid) != string(want.Kid) || string(got.X) != string(want.X) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestWriteReadMessageMultipleOnOneStream(t *testing.T) {
	var buf bytes.Buffer
	first := RecoverRequest{Kid: []byte("k1")}
	second := RecoverResponse{XOnly: []byte("x1")}

	if err := WriteMessage(&buf, first); err != nil {
		t.Fatalf("WriteMessage 1: %v", err)
	}
	if err := WriteMessage(&buf, second); err != nil {
		t.Fatalf("WriteMessage 2: %v", err)
	}

	var gotFirst RecoverRequest
	if err := ReadMessage(&buf, &gotFirst); err != nil {
		t.Fatalf("ReadMessage 1: %v", err)
	}
	var gotSecond RecoverResponse
	if err := ReadMessage(&buf, &gotSecond); err != nil {
		t.Fatalf("ReadMessage 2: %v", err)
	}

	if string(gotFirst.Kid) != "k1" || string(gotSecond.XOnly) != "x1" {
		t.Fatalf("messages did not round-trip in order: %+v, %+v", gotFirst, gotSecond)
	}
}

func TestReadMessageRejectsOversized(t *testing.T) {
	var buf bytes.Buffer
	// A length prefix far bigger than any real MR-1 message, without
	// the matching body: a malicious or buggy peer must not be able to
	// make the reader allocate gigabytes or block forever.
	buf.Write([]byte{0x7f, 0xff, 0xff, 0xff})

	var v RecoverRequest
	err := ReadMessage(&buf, &v)
	if err == nil {
		t.Fatal("expected an error for an oversized length prefix, got nil")
	}
}

func TestReadMessageRejectsTruncatedStream(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, RecoverRequest{Kid: []byte("k")}); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	truncated := bytes.NewReader(buf.Bytes()[:buf.Len()-1])

	var v RecoverRequest
	err := ReadMessage(truncated, &v)
	if err == nil {
		t.Fatal("expected an error for a truncated message, got nil")
	}
	if err != io.ErrUnexpectedEOF && err != io.EOF {
		// Either is acceptable, both mean "the stream ended early";
		// just make sure it is not silently treated as success.
		t.Logf("truncated read failed with: %v (not EOF/ErrUnexpectedEOF, but still an error, fine)", err)
	}
}

// The machine's final word has to survive the round trip, including
// the failure case: a key holder that cannot tell "unlocked" from
// "did not unlock" is the bug this message exists to fix.
func TestRecoverResultRoundTrip(t *testing.T) {
	for _, want := range []RecoverResult{
		{OK: true},
		{OK: false, Error: "recovered secret does not open /dev/vda3"},
	} {
		var buf bytes.Buffer
		if err := WriteMessage(&buf, want); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
		var got RecoverResult
		if err := ReadMessage(&buf, &got); err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if got.OK != want.OK || got.Error != want.Error {
			t.Errorf("round trip = %+v, want %+v", got, want)
		}
	}
}

// What the machine says it will do with the answer, in its greeting. Empty is an ordinary unlock, and is not sent
// at all, so that a greeting without the field and a greeting for an unlock look the same on the wire.
func TestAHelloCarriesAPurposeOnlyWhenThereIsOne(t *testing.T) {
	var unlock bytes.Buffer
	if err := WriteMessage(&unlock, Hello{Name: "box"}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(unlock.Bytes(), []byte("purpose")) {
		t.Errorf("an unlock greeting carries a purpose: %q", unlock.Bytes())
	}
	for _, purpose := range []string{PurposeEnrol, PurposePair, PurposeExtract} {
		var buf bytes.Buffer
		if err := WriteMessage(&buf, Hello{Name: "box", Purpose: purpose}); err != nil {
			t.Fatal(err)
		}
		var got Hello
		if err := ReadMessage(&buf, &got); err != nil {
			t.Fatal(err)
		}
		if got.Name != "box" || got.Purpose != purpose {
			t.Errorf("round trip = %+v, want purpose %q", got, purpose)
		}
	}
}

func TestOnlyTheNamedPurposesAreKnown(t *testing.T) {
	for _, p := range []string{PurposeEnrol, PurposePair, PurposeExtract} {
		if !KnownPurpose(p) {
			t.Errorf("%q is not known", p)
		}
	}
	for _, p := range []string{"", "unlock", "Enrol", "something a newer machine says"} {
		if KnownPurpose(p) {
			t.Errorf("%q is known", p)
		}
	}
}
