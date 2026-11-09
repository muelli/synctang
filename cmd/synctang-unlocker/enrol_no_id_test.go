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
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "WARNING") {
			warning += line + "\n"
		}
	}
	for _, want := range []string{"no transport id", "keyslot " + slot, "--remove-keyslot " + slot} {
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
	if !strings.Contains(out.String(), "will not listen") || !strings.Contains(out.String(), "--recipient-transport-id") {
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
	code := run([]string{"enrol", "--device", device, "--remove-keyslot", slot, "--existing-passphrase-file", passFile}, &out, &errOut)
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
	if code := run([]string{"enrol", "--device", device, "--remove-keyslot", "7", "--existing-passphrase-file", passFile}, &out, &errOut); code == 0 {
		t.Fatalf("exit 0: %s", out.String())
	}
	if tokenBytes(t, device) != before {
		t.Error("the token changed")
	}
	// Not together with --remove: two instructions for one run.
	if code := run([]string{"enrol", "--device", device, "--remove", testKH1, "--remove-keyslot", "1", "--existing-passphrase-file", passFile}, &out, &errOut); code != 2 {
		t.Errorf("--remove with --remove-keyslot: exit %d, want 2", code)
	}
}
