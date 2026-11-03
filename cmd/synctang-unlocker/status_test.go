// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// "Who can unlock this machine, and which identity does it answer to?"
// is a question an operator has to be able to ask without reading LUKS2
// token JSON by hand, and has to be able to answer before revoking a
// key holder: enrol --remove takes a transport id, and getting it
// wrong destroys the wrong keyslot.
func TestStatusListsRecipientsAndTheMachineIdentity(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	var ids []string
	for _, name := range []string{"laptop", "phone"} {
		s, err := g.RandomScalar()
		if err != nil {
			t.Fatalf("RandomScalar: %v", err)
		}
		S, err := g.ScalarBaseMult(s)
		if err != nil {
			t.Fatalf("ScalarBaseMult: %v", err)
		}
		cert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), name+".pem"))
		if err != nil {
			t.Fatalf("LoadOrCreateCert: %v", err)
		}
		id := protocol.NewDeviceID(cert.Certificate[0]).String()
		ids = append(ids, id)
		if err := enrolRecipient(device, existingPassphrase, S, "study-machine", "MACHINE-ID", id); err != nil {
			t.Fatalf("enrolRecipient: %v", err)
		}
	}

	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	cert, err := transport.LoadOrCreateCert(machineKey)
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(cert.Certificate[0]).String()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr.String())
	}
	out := stdout.String()

	if !strings.Contains(out, machineID) {
		t.Errorf("status should say which identity this machine answers to, got:\n%s", out)
	}
	for _, id := range ids {
		if !strings.Contains(out, id) {
			t.Errorf("status omitted enrolled recipient %s, got:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "study-machine") {
		t.Errorf("status should show the machine name key holders are shown, got:\n%s", out)
	}
	// The keyslot each recipient owns is what makes revocation safe to
	// reason about.
	if !strings.Contains(out, "keyslot") {
		t.Errorf("status should say which keyslot each recipient holds, got:\n%s", out)
	}
}

// A device with no mr-1 token is not an error: it is a machine nobody
// has enrolled yet, and saying so plainly beats a failure.
func TestStatusOnAnUnenrolledDevice(t *testing.T) {
	device := formatLoopbackImage(t, []byte("existing-test-passphrase"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--device", device, "--machine-key-file", filepath.Join(t.TempDir(), "m.pem")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no key holder is enrolled") {
		t.Errorf("expected a plain statement that nothing is enrolled, got:\n%s", stdout.String())
	}
}

// Reading status must not create a machine identity as a side effect:
// an operator asking a question should not change the answer.
func TestStatusDoesNotInventAMachineIdentity(t *testing.T) {
	device := formatLoopbackImage(t, []byte("existing-test-passphrase"))
	machineKey := filepath.Join(t.TempDir(), "absent.pem")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &stdout, &stderr); code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(machineKey); !os.IsNotExist(err) {
		t.Fatalf("status created %s; reading state must not change it", machineKey)
	}
	if !strings.Contains(stdout.String(), "not created yet") {
		t.Errorf("expected status to say the identity does not exist yet, got:\n%s", stdout.String())
	}
}

var _ = hex.EncodeToString
