// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// T3.x: luksKeyMaterial must never produce a NUL byte, whatever p
// contains, and must round-trip exactly: hex satisfies both, and this
// is the one property the whole fix depends on.
func TestLuksKeyMaterialHasNoNulBytesAndRoundTrips(t *testing.T) {
	p := []byte{0x7d, 0x00, 0x4b, 0x8b, 0x00, 0x01, 0xff, 0x00}

	got := luksKeyMaterial(p)

	if bytes.IndexByte(got, 0) >= 0 {
		t.Fatalf("luksKeyMaterial(%x) = %q, contains a NUL byte", p, got)
	}

	decoded, err := hex.DecodeString(string(got))
	if err != nil {
		t.Fatalf("luksKeyMaterial did not produce valid hex: %v", err)
	}
	if !bytes.Equal(decoded, p) {
		t.Fatalf("round trip: got %x, want %x", decoded, p)
	}
}
