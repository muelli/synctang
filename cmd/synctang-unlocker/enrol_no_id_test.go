// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An entry without a transport id is what an older enrol could leave behind. It can neither be
// dialled nor named to --remove by id, so status has to say so and name the keyslot, and there
// has to be a way to remove it by that keyslot.

// idlessImage is a volume with one key holder that has an id and one that has none.
func idlessImage(t *testing.T) (device string, pass []byte, passFile, withoutIDSlot string) {
	t.Helper()
	pass = []byte("existing-test-passphrase")
	device = formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	tok := exportTokenForTest(t, device)
	for i, rec := range tok.Recipients {
		if id, ok := rec.TransportDeviceID(); !ok || id == "" {
			withoutIDSlot = tok.Keyslots[i]
		}
	}
	if withoutIDSlot == "" {
		t.Fatal("no entry without an id was made")
	}
	passFile = filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(passFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	return device, pass, passFile, withoutIDSlot
}

func TestStatusWarnsAboutAnEntryWithoutATransportIDAndSaysHowToRemoveIt(t *testing.T) {
	device, _, _, slot := idlessImage(t)
	var out, errOut bytes.Buffer
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	if code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %q", code, errOut.String())
	}
	var warning string
	inWarning := false
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "WARNING") {
			inWarning = true
		} else if !strings.HasPrefix(line, " ") {
			inWarning = false
		}
		if inWarning {
			warning += line + "\n"
		}
	}
	// A warning is wrapped to fit a terminal, so a phrase may be broken across lines.
	warning = strings.Join(strings.Fields(warning), " ")
	for _, want := range []string{"no transport id", "keyslot " + slot, "synctang-unlocker revoke", "--keyslot " + slot} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning lacks %q:\n%s", want, out.String())
		}
	}
}

func TestStatusSaysTheAgentWillNotRunWhenNoEntryHasATransportID(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	if code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %q", code, errOut.String())
	}
	if flat := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(flat, "will not listen") || !strings.Contains(flat, "--recipient-transport-id") {
		t.Errorf("nothing said about the agent refusing to run:\n%s", out.String())
	}
}

func TestStatusIsQuietWhenEveryEntryHasATransportID(t *testing.T) {
	// TestStatusIsQuietWithoutDuplicates covers this case with one enrolled key holder; this
	// is the same with the new warnings in place.
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	run([]string{"status", "--device", device, "--machine-key-file", filepath.Join(t.TempDir(), "machine.pem")}, &out, &errOut)
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("a warning where there is nothing to warn about:\n%s", out.String())
	}
}

func TestRemoveKeyslotRemovesAnEntryThatHasNoTransportID(t *testing.T) {
	device, _, passFile, slot := idlessImage(t)
	slotsBefore := usedSlotCount(t, device)
	var out, errOut bytes.Buffer
	code := run([]string{"revoke", "--device", device, "--keyslot", slot, "--existing-passphrase-file", passFile}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}
	if got := usedSlotCount(t, device); got != slotsBefore-1 {
		t.Errorf("keyslots %d -> %d, want one fewer", slotsBefore, got)
	}
	tok := exportTokenForTest(t, device)
	if len(tok.Recipients) != 1 {
		t.Fatalf("%d entries left, want the one with an id", len(tok.Recipients))
	}
	if _, ok := tok.Recipients[0].TransportDeviceID(); !ok {
		t.Error("the wrong entry was removed")
	}
	for _, s := range tok.Keyslots {
		if s == slot {
			t.Errorf("keyslot %s is still in the token", slot)
		}
	}
}

func TestRemoveKeyslotRefusesWhatItCannotFind(t *testing.T) {
	device, _, passFile, _ := idlessImage(t)
	before := tokenBytes(t, device)
	var out, errOut bytes.Buffer
	if code := run([]string{"revoke", "--device", device, "--keyslot", "7", "--existing-passphrase-file", passFile}, &out, &errOut); code == 0 {
		t.Fatalf("exit 0: %s", out.String())
	}
	if tokenBytes(t, device) != before {
		t.Error("the token changed")
	}
}

// "they are in keyslots: " followed by nothing is no help. When no key holder is enrolled at all, say that; when
// some are, name where.
func TestRemoveKeyslotSaysWhenNoKeyHolderIsEnrolledAtAll(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	slot := keyslotOfRecipient(t, device, testKH1)
	passFile := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(passFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeRecipientByKeyslot(device, pass, slot); err != nil {
		t.Fatal(err)
	}
	// The token is still there, with nobody in it.
	err := removeRecipientByKeyslot(device, pass, "0")
	if err == nil || !strings.Contains(err.Error(), "no key holder is enrolled on this device") || strings.Contains(err.Error(), "keyslots: )") {
		t.Errorf("%v", err)
	}

	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH2); err != nil {
		t.Fatal(err)
	}
	err = removeRecipientByKeyslot(device, pass, "7")
	if err == nil || !strings.Contains(err.Error(), "keyslot 7 holds no enrolled key holder") || !strings.Contains(err.Error(), "keyslot "+keyslotOfRecipient(t, device, testKH2)) {
		t.Errorf("with some enrolled: %v", err)
	}
}
