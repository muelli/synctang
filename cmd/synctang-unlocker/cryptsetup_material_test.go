// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"testing"
)

// The keyslot passphrase is the hex of the secret, as bytes, made without a string on the way: a string cannot
// be wiped, and the secret is only as short-lived as its last copy.
func TestKeyMaterialIsTheHexOfTheSecret(t *testing.T) {
	secret := []byte{0x00, 0x01, 0xfe, 0xff, 0x10}
	got := luksKeyMaterial(secret)
	if string(got) != hex.EncodeToString(secret) {
		t.Errorf("%q", got)
	}
	if len(got) != hex.EncodedLen(len(secret)) || cap(got) != len(got) {
		t.Errorf("length %d capacity %d: a copy was made on the way", len(got), cap(got))
	}
}
