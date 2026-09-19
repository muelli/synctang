// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", ""); err != nil {
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

	if err := enrolRecipient(device, existingPassphrase, S1, "test-machine", "MACHINE-ID", ""); err != nil {
		t.Fatalf("first enrolRecipient: %v", err)
	}
	if err := enrolRecipient(device, existingPassphrase, S2, "test-machine", "MACHINE-ID", ""); err != nil {
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

// A8: revoking one recipient must destroy exactly its own keyslot,
// leaving the other recipient able to recover its own P and open the
// device with it, exactly as before.
func TestRemoveRecipientRevokesOnlyThatOne(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s1, _ := g.RandomScalar()
	S1, _ := g.ScalarBaseMult(s1)
	s2, _ := g.RandomScalar()
	S2, _ := g.ScalarBaseMult(s2)

	if err := enrolRecipient(device, existingPassphrase, S1, "test-machine", "MACHINE-ID", "KH-1"); err != nil {
		t.Fatalf("enrolRecipient 1: %v", err)
	}
	if err := enrolRecipient(device, existingPassphrase, S2, "test-machine", "MACHINE-ID", "KH-2"); err != nil {
		t.Fatalf("enrolRecipient 2: %v", err)
	}

	beforeTok := exportTokenForTest(t, device)
	if len(beforeTok.Recipients) != 2 {
		t.Fatalf("expected 2 recipients before removal, got %d", len(beforeTok.Recipients))
	}
	var rec1 mrcore.Recipient
	for _, rec := range beforeTok.Recipients {
		if id, _ := rec.TransportDeviceID(); id == "KH-1" {
			rec1 = rec
		}
	}

	p1 := recoverPForTest(t, g, rec1, s1)

	if err := removeRecipient(device, existingPassphrase, "KH-1"); err != nil {
		t.Fatalf("removeRecipient: %v", err)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	slots := usedKeyslots(string(dump))
	if len(slots) != 2 {
		t.Fatalf("expected 2 keyslots after removal (existing passphrase + KH-2), got %v", slots)
	}

	afterTok := exportTokenForTest(t, device)
	if len(afterTok.Recipients) != 1 {
		t.Fatalf("expected 1 recipient after removal, got %d", len(afterTok.Recipients))
	}
	gotID, ok := afterTok.Recipients[0].TransportDeviceID()
	if !ok || gotID != "KH-2" {
		t.Fatalf("expected the surviving recipient to be KH-2, got (%q, %v)", gotID, ok)
	}

	// enrolRecipient keys each slot with luksKeyMaterial(P) (hex), not
	// raw P; test-passphrase must use the same encoding.
	keyMaterial1 := luksKeyMaterial(p1)
	if _, err := runCommand(keyMaterial1, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(keyMaterial1)), device); err == nil {
		t.Fatal("the revoked recipient's P still opens the device")
	}

	p2 := recoverPForTest(t, g, afterTok.Recipients[0], s2)
	keyMaterial2 := luksKeyMaterial(p2)
	if _, err := runCommand(keyMaterial2, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(keyMaterial2)), device); err != nil {
		t.Fatalf("the surviving recipient's P no longer opens the device: %v", err)
	}
}

// removeRecipient with an unknown transport id must fail rather than
// silently doing nothing or, worse, guessing which keyslot to destroy.
func TestRemoveRecipientUnknownIDFails(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, _ := g.RandomScalar()
	S, _ := g.ScalarBaseMult(s)
	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", "KH-1"); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	if err := removeRecipient(device, existingPassphrase, "NO-SUCH-ID"); err == nil {
		t.Fatal("removeRecipient succeeded for an id that was never enrolled")
	}
}

func exportTokenForTest(t *testing.T, device string) mrcore.Token {
	t.Helper()
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
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
	return tok
}

func recoverPForTest(t *testing.T, g mrcore.Group, rec mrcore.Recipient, s []byte) []byte {
	t.Helper()
	e, X, err := mrcore.ChallengeStart(g, rec.C)
	if err != nil {
		t.Fatalf("ChallengeStart: %v", err)
	}
	y, err := g.ScalarMult(X, s)
	if err != nil {
		t.Fatalf("key holder ScalarMult: %v", err)
	}
	p, err := mrcore.FinishFullPoint(g, e, rec, y)
	if err != nil {
		t.Fatalf("FinishFullPoint: %v", err)
	}
	return p
}

func itoaForTest(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A non-empty recipientTransportID must round-trip through the exported
// token as Recipient.Transport, in a shape TransportDeviceID can parse
// back out: this is what lets the agent later restrict Listen's
// AuthorizedPeers to exactly the enrolled key holders.
func TestEnrolRecordsRecipientTransportID(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, _ := g.RandomScalar()
	S, _ := g.ScalarBaseMult(s)

	const wantID = "KEYHOLDER-DEVICE-ID"
	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", wantID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
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
		t.Fatalf("expected 1 recipient, got %d", len(tok.Recipients))
	}
	gotID, ok := tok.Recipients[0].TransportDeviceID()
	if !ok || gotID != wantID {
		t.Fatalf("Recipient.TransportDeviceID() = (%q, %v), want (%q, true)", gotID, ok, wantID)
	}
}

// An empty recipientTransportID must not fail enrolment, and must leave
// Transport unset.
func TestEnrolEmptyRecipientTransportIDIsAllowed(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, _ := g.RandomScalar()
	S, _ := g.ScalarBaseMult(s)

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", ""); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	tokenIDs := tokenIDsOfType(string(dump), mrTokenType)
	exported, err := runCommand(nil, "cryptsetup", "token", "export",
		"--token-id", itoaForTest(tokenIDs[0]), device)
	if err != nil {
		t.Fatalf("token export: %v", err)
	}

	var tok mrcore.Token
	if err := json.Unmarshal(exported, &tok); err != nil {
		t.Fatalf("unmarshal exported token: %v", err)
	}
	if _, ok := tok.Recipients[0].TransportDeviceID(); ok {
		t.Fatalf("expected no transport device id when recipientTransportID is empty")
	}
}
