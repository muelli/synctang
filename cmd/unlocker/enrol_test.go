// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// cryptsetupOrSkip finds cryptsetup (same search as resolveCommand:
// PATH, then /usr/sbin, then /sbin) or skips the test. Header
// operations (format, addKey, token import/export, test-passphrase)
// need neither root nor the dm_crypt kernel module, verified against
// cryptsetup 2.8.4; only actually opening/mapping a device would.
// Kept as a skip rather than a hard requirement in case a locked-down
// runner lacks the binary entirely.
func cryptsetupOrSkip(t *testing.T) {
	t.Helper()
	path := resolveCommand("cryptsetup")
	if _, err := os.Stat(path); err != nil {
		t.Skip("cryptsetup not found, skipping loopback LUKS2 test")
	}
}

func formatLoopbackImage(t *testing.T, existingPassphrase []byte) string {
	t.Helper()
	cryptsetupOrSkip(t)

	device := filepath.Join(t.TempDir(), "test.img")
	if _, err := runCommand(nil, "truncate", "-s", "32M", device); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := runCommand(existingPassphrase, "cryptsetup", "luksFormat",
		"--type", "luks2", "--batch-mode", "--key-file=-", device); err != nil {
		t.Fatalf("luksFormat: %v", err)
	}
	return device
}

// T3.1: enrol adds a keyslot and a token; cryptsetup token export
// shows it.
func TestEnrolAddsKeyslotAndToken(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID"); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	slots := usedKeyslots(string(dump))
	if len(slots) != 2 {
		t.Fatalf("expected 2 keyslots (existing passphrase + P), got %v", slots)
	}
	tokenIDs := tokenIDsOfType(string(dump), mrTokenType)
	if len(tokenIDs) != 1 {
		t.Fatalf("expected exactly one mr-1 token, got %v", tokenIDs)
	}

	exported, err := runCommand(nil, "cryptsetup", "token", "export",
		"--token-id", itoaForTest(tokenIDs[0]), device)
	if err != nil {
		t.Fatalf("token export: %v", err)
	}

	var tok mrcore.Token
	if err := json.Unmarshal(exported, &tok); err != nil {
		t.Fatalf("unmarshal exported token: %v", err)
	}
	if len(tok.Recipients) != 1 {
		t.Fatalf("expected 1 recipient in the exported token, got %d", len(tok.Recipients))
	}
	if hex.EncodeToString(tok.Recipients[0].S) != hex.EncodeToString(S) {
		t.Fatalf("exported recipient's S does not match the enrolled public key")
	}
	if len(tok.Keyslots) != 1 {
		t.Fatalf("expected the token to name exactly 1 keyslot, got %v", tok.Keyslots)
	}
	if tok.Machine.Name != "test-machine" || tok.Machine.TransportID != "MACHINE-ID" {
		t.Fatalf("machine info did not round-trip: %+v", tok.Machine)
	}
}

// A second enrolment for a different recipient must append to the
// same token rather than creating a duplicate, and both recipients'
// keyslots must coexist.
func TestEnrolSecondRecipientAppends(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s1, _ := g.RandomScalar()
	S1, _ := g.ScalarBaseMult(s1)
	s2, _ := g.RandomScalar()
	S2, _ := g.ScalarBaseMult(s2)

	if err := enrolRecipient(device, existingPassphrase, S1, "test-machine", "MACHINE-ID"); err != nil {
		t.Fatalf("first enrolRecipient: %v", err)
	}
	if err := enrolRecipient(device, existingPassphrase, S2, "test-machine", "MACHINE-ID"); err != nil {
		t.Fatalf("second enrolRecipient: %v", err)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	slots := usedKeyslots(string(dump))
	if len(slots) != 3 {
		t.Fatalf("expected 3 keyslots (existing passphrase + 2 recipients), got %v", slots)
	}
	tokenIDs := tokenIDsOfType(string(dump), mrTokenType)
	if len(tokenIDs) != 1 {
		t.Fatalf("expected exactly one mr-1 token after two enrolments, got %v", tokenIDs)
	}

	exported, err := runCommand(nil, "cryptsetup", "token", "export",
		"--token-id", itoaForTest(tokenIDs[0]), device)
	if err != nil {
		t.Fatalf("token export: %v", err)
	}
	var tok mrcore.Token
	if err := json.Unmarshal(exported, &tok); err != nil {
		t.Fatalf("unmarshal exported token: %v", err)
	}
	if len(tok.Recipients) != 2 {
		t.Fatalf("expected 2 recipients in the merged token, got %d", len(tok.Recipients))
	}
	if len(tok.Keyslots) != 2 {
		t.Fatalf("expected 2 keyslots named in the merged token, got %v", tok.Keyslots)
	}
}

func itoaForTest(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
