// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func passphraseFile(t *testing.T, passphrase string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(path, []byte(passphrase), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// "unlocker pair" used to read the passphrase and print its code without ever
// testing it, so a typo surfaced only at the end: after the phone had scanned
// the code and the person had confirmed, when the enrolment failed. It checks
// the passphrase against the header first, before it makes an identity or
// shows anything.
func TestPairRefusesAWrongPassphraseBeforeShowingACode(t *testing.T) {
	device := formatLoopbackImage(t, []byte("the-right-passphrase"))
	machineKey := filepath.Join(t.TempDir(), "machine.pem")

	var stdout, stderr bytes.Buffer
	code := run([]string{"pair", "--device", device,
		"--existing-passphrase-file", passphraseFile(t, "the-wrong-passphrase"),
		"--machine-key-file", machineKey,
		"--direct-addr", freeTestAddr(t), "--timeout", "2s"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit 0 with a wrong passphrase; stdout %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "synctang://pair") {
		t.Errorf("a code was shown for a passphrase that does not open the volume:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "passphrase") {
		t.Errorf("stderr should say the passphrase is wrong, got %q", stderr.String())
	}
	if _, err := os.Stat(machineKey); err == nil {
		t.Error("a machine identity was created although nothing was going to be enrolled")
	}
}

// The check must not get in the way of the ordinary case.
func TestPairWithTheRightPassphraseStillShowsItsCode(t *testing.T) {
	device := formatLoopbackImage(t, []byte("the-right-passphrase"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"pair", "--device", device,
		"--existing-passphrase-file", passphraseFile(t, "the-right-passphrase"),
		"--machine-key-file", filepath.Join(t.TempDir(), "machine.pem"),
		"--direct-addr", freeTestAddr(t), "--timeout", "1s"}, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "synctang://pair?") {
		t.Errorf("no pairing link was shown (exit %d):\nstdout %q\nstderr %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "nobody paired") {
		t.Errorf("with nobody scanning, the window should end with 'nobody paired', got %q", stderr.String())
	}
}
