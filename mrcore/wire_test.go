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
