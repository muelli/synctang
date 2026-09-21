// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// Rotating a key holder's key must never leave a window in which nobody
// can unlock the volume, and must not depend on the operator
// remembering to revoke the old key afterwards. The old P must stop
// opening the device and the new one must start, in a single step.
//
// Deliberately rotates to the *same* transport id, which is the normal
// case: a key holder that rotates its MR-1 key keeps its transport
// identity. That makes the old and new entries indistinguishable by
// transport id, so an implementation that revokes by id rather than by
// keyslot can revoke the wrong one, which this test catches.
func TestReplaceRecipientRotatesAKeyInOneStep(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	sOld, _ := g.RandomScalar()
	SOld, _ := g.ScalarBaseMult(sOld)
	sNew, _ := g.RandomScalar()
	SNew, _ := g.ScalarBaseMult(sNew)

	if err := enrolRecipient(device, existingPassphrase, SOld, "test-machine", "MACHINE-ID", "KH-1"); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	beforeTok := exportTokenForTest(t, device)
	pOld := recoverPForTest(t, g, beforeTok.Recipients[0], sOld)

	if err := replaceRecipient(device, existingPassphrase, SNew, "KH-1", "KH-1"); err != nil {
		t.Fatalf("replaceRecipient: %v", err)
	}

	afterTok := exportTokenForTest(t, device)
	if len(afterTok.Recipients) != 1 {
		t.Fatalf("expected exactly 1 recipient after rotation, got %d", len(afterTok.Recipients))
	}
	if gotID, ok := afterTok.Recipients[0].TransportDeviceID(); !ok || gotID != "KH-1" {
		t.Fatalf("expected the rotated recipient to keep transport id KH-1, got (%q, %v)", gotID, ok)
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	if slots := usedKeyslots(string(dump)); len(slots) != 2 {
		t.Fatalf("expected 2 keyslots after rotation (existing passphrase + the new key), got %v", slots)
	}

	if opensDeviceForTest(t, device, pOld) {
		t.Fatal("the rotated-away key still opens the device")
	}
	pNew := recoverPForTest(t, g, afterTok.Recipients[0], sNew)
	if !opensDeviceForTest(t, device, pNew) {
		t.Fatal("the new key does not open the device")
	}
}

// Rotating one key holder must leave every other key holder alone.
func TestReplaceRecipientLeavesOtherKeyHoldersAlone(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s1, _ := g.RandomScalar()
	S1, _ := g.ScalarBaseMult(s1)
	s2, _ := g.RandomScalar()
	S2, _ := g.ScalarBaseMult(s2)
	s1b, _ := g.RandomScalar()
	S1b, _ := g.ScalarBaseMult(s1b)

	if err := enrolRecipient(device, existingPassphrase, S1, "test-machine", "MACHINE-ID", "KH-1"); err != nil {
		t.Fatalf("enrolRecipient 1: %v", err)
	}
	if err := enrolRecipient(device, existingPassphrase, S2, "test-machine", "MACHINE-ID", "KH-2"); err != nil {
		t.Fatalf("enrolRecipient 2: %v", err)
	}

	if err := replaceRecipient(device, existingPassphrase, S1b, "KH-1", "KH-1"); err != nil {
		t.Fatalf("replaceRecipient: %v", err)
	}

	afterTok := exportTokenForTest(t, device)
	if len(afterTok.Recipients) != 2 {
		t.Fatalf("expected 2 recipients after rotating one of them, got %d", len(afterTok.Recipients))
	}

	var rec2 mrcore.Recipient
	found := false
	for _, rec := range afterTok.Recipients {
		if id, _ := rec.TransportDeviceID(); id == "KH-2" {
			rec2, found = rec, true
		}
	}
	if !found {
		t.Fatal("KH-2 disappeared when KH-1 was rotated")
	}
	if !opensDeviceForTest(t, device, recoverPForTest(t, g, rec2, s2)) {
		t.Fatal("KH-2's key stopped opening the device when KH-1 was rotated")
	}
}

// A typo in the id being rotated must be refused before anything is
// changed. Rotation is otherwise the one operation that both adds and
// destroys a keyslot, so a half-applied rotation is the worst outcome
// available: a new key enrolled that nobody asked for, and no way to
// tell from the token which of the two entries was meant to survive.
func TestReplaceRecipientRefusesAnUnknownIDBeforeChangingAnything(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s1, _ := g.RandomScalar()
	S1, _ := g.ScalarBaseMult(s1)
	sNew, _ := g.RandomScalar()
	SNew, _ := g.ScalarBaseMult(sNew)

	if err := enrolRecipient(device, existingPassphrase, S1, "test-machine", "MACHINE-ID", "KH-1"); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	err := replaceRecipient(device, existingPassphrase, SNew, "KH-TYPO", "KH-1")
	if err == nil {
		t.Fatal("replaceRecipient accepted an unknown transport id")
	}

	afterTok := exportTokenForTest(t, device)
	if len(afterTok.Recipients) != 1 {
		t.Fatalf("a failed rotation changed the token: expected 1 recipient, got %d", len(afterTok.Recipients))
	}
	dump, err2 := runCommand(nil, "cryptsetup", "luksDump", device)
	if err2 != nil {
		t.Fatalf("luksDump: %v", err2)
	}
	if slots := usedKeyslots(string(dump)); len(slots) != 2 {
		t.Fatalf("a failed rotation changed the keyslots: got %v", slots)
	}
	if !opensDeviceForTest(t, device, recoverPForTest(t, g, afterTok.Recipients[0], s1)) {
		t.Fatal("a failed rotation broke the existing key holder")
	}
}

// opensDeviceForTest reports whether p, encoded the way enrolRecipient
// keys its slots, opens device.
func opensDeviceForTest(t *testing.T, device string, p []byte) bool {
	t.Helper()
	keyMaterial := luksKeyMaterial(p)
	_, err := runCommand(keyMaterial, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", fmt.Sprintf("--keyfile-size=%d", len(keyMaterial)), device)
	return err == nil
}
