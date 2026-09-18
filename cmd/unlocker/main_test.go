// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

func TestRunEnrolRequiresFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"enrol"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit with no flags given")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"frobnicate"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit for an unknown command")
	}
}

func TestRunEnrolEndToEnd(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	passphraseFile := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(passphraseFile, existingPassphrase, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	S, err := mrcore.P256().ScalarBaseMult(mustScalar(t))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"enrol",
		"--device", device,
		"--pubkey", hex.EncodeToString(S),
		"--existing-passphrase-file", passphraseFile,
		"--name", "cli-test-machine",
		"--transport-id", "CLI-TEST-ID",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run enrol: exit %d, stderr %q", code, stderr.String())
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	if len(tokenIDsOfType(string(dump), mrTokenType)) != 1 {
		t.Fatalf("expected exactly one mr-1 token after CLI enrol")
	}
}

func mustScalar(t *testing.T) []byte {
	t.Helper()
	s, err := mrcore.P256().RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	return s
}
