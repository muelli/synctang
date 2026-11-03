// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strconv"
	"testing"
)

const sampleDump = `LUKS header information
Version:       	2
Epoch:         	4
Metadata area: 	16384 [bytes]
Keyslots area: 	16744448 [bytes]
UUID:          	442828ce-5625-4ebb-a914-732071988643
Label:         	(no label)
Subsystem:     	(no subsystem)
Flags:       	(no flags)

Data segments:
  0: crypt
	offset: 16777216 [bytes]
	length: (whole device)
	cipher: aes-xts-plain64
	sector: 4096 [bytes]

Keyslots:
  0: luks2
	Key:        512 bits
	Priority:   normal
	Cipher:     aes-xts-plain64
  1: luks2
	Key:        512 bits
	Priority:   normal
	Cipher:     aes-xts-plain64
Tokens:
  0: clevis
  2: mr-1
	Keyslot:    1
Digests:
  0: pbkdf2
	Hash:       sha256
`

func TestUsedKeyslots(t *testing.T) {
	got := usedKeyslots(sampleDump)
	want := []int{0, 1}
	if len(got) != len(want) {
		t.Fatalf("usedKeyslots: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("usedKeyslots: got %v want %v", got, want)
		}
	}
}

func TestLowestFreeKeyslot(t *testing.T) {
	got, err := lowestFreeKeyslot(sampleDump)
	if err != nil {
		t.Fatalf("lowestFreeKeyslot: %v", err)
	}
	if got != 2 {
		t.Fatalf("lowestFreeKeyslot: got %d want 2", got)
	}
}

func TestLowestFreeKeyslotAllUsed(t *testing.T) {
	// Every slot from 0 to 31 occupied.
	dump := "Keyslots:\n"
	for i := 0; i < 32; i++ {
		dump += "  " + strconv.Itoa(i) + ": luks2\n"
	}
	if _, err := lowestFreeKeyslot(dump); err == nil {
		t.Fatal("expected an error with all 32 keyslots occupied, got nil")
	}
}

func TestTokenIDsOfType(t *testing.T) {
	got := tokenIDsOfType(sampleDump, "mr-1")
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("tokenIDsOfType(mr-1): got %v want [2]", got)
	}

	got = tokenIDsOfType(sampleDump, "clevis")
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("tokenIDsOfType(clevis): got %v want [0]", got)
	}

	got = tokenIDsOfType(sampleDump, "systemd-tpm2")
	if len(got) != 0 {
		t.Fatalf("tokenIDsOfType(systemd-tpm2): got %v want []", got)
	}
}

func TestTokenIDsOfTypeNoTokensSection(t *testing.T) {
	got := tokenIDsOfType("Keyslots:\n  0: luks2\nDigests:\n  0: pbkdf2\n", "mr-1")
	if len(got) != 0 {
		t.Fatalf("expected no tokens, got %v", got)
	}
}
