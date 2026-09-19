// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		"--recipient-transport-id", "CLI-TEST-RECIPIENT-ID",
		"--machine-key-file", filepath.Join(t.TempDir(), "machine.pem"),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run enrol: exit %d, stderr %q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "this machine's transport id:") {
		t.Errorf("expected enrol to print this machine's own transport id, got stdout %q", stdout.String())
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	tokenIDs := tokenIDsOfType(string(dump), mrTokenType)
	if len(tokenIDs) != 1 {
		t.Fatalf("expected exactly one mr-1 token after CLI enrol")
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
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != "CLI-TEST-RECIPIENT-ID" {
		t.Fatalf("recipient transport id = (%q, %v), want (%q, true)", id, ok, "CLI-TEST-RECIPIENT-ID")
	}
}

// A8, exercised at the CLI level: "enrol --remove" revokes exactly the
// named recipient and leaves the other one enrolled.
func TestRunEnrolRemoveEndToEnd(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	passphraseFile := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(passphraseFile, existingPassphrase, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	S1, err := mrcore.P256().ScalarBaseMult(mustScalar(t))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	S2, err := mrcore.P256().ScalarBaseMult(mustScalar(t))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	for _, recipient := range []struct {
		pubkey []byte
		id     string
	}{
		{S1, "KH-1"},
		{S2, "KH-2"},
	} {
		var stdout, stderr bytes.Buffer
		code := run([]string{
			"enrol",
			"--device", device,
			"--pubkey", hex.EncodeToString(recipient.pubkey),
			"--existing-passphrase-file", passphraseFile,
			"--recipient-transport-id", recipient.id,
			"--machine-key-file", filepath.Join(t.TempDir(), "machine.pem"),
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run enrol %s: exit %d, stderr %q", recipient.id, code, stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"enrol",
		"--device", device,
		"--existing-passphrase-file", passphraseFile,
		"--remove", "KH-1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run enrol --remove: exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "revoked recipient KH-1") {
		t.Errorf("expected confirmation of the revocation, got stdout %q", stdout.String())
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
	if len(tok.Recipients) != 1 {
		t.Fatalf("expected 1 recipient after --remove, got %d", len(tok.Recipients))
	}
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != "KH-2" {
		t.Fatalf("surviving recipient = (%q, %v), want (%q, true)", id, ok, "KH-2")
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

// A passphrase file created with a text editor, or piped via a shell
// here-string, almost always carries a trailing newline that is not
// part of the passphrase a human actually typed at cryptsetup's own
// interactive prompt (the terminal driver strips that newline before
// the passphrase reaches cryptsetup). readPassphrase must strip
// exactly one trailing newline (and a preceding \r, for files written
// on Windows) so --existing-passphrase-file behaves the way a human
// expects, not like a raw cryptsetup --key-file (which deliberately
// treats trailing bytes as significant).
func TestReadPassphraseStripsOneTrailingNewline(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"no newline", "passphrase", "passphrase"},
		{"unix newline", "passphrase\n", "passphrase"},
		{"windows newline", "passphrase\r\n", "passphrase"},
		{"only one newline stripped", "passphrase\n\n", "passphrase\n"},
		{"internal newline kept", "pass\nphrase\n", "pass\nphrase"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, "passphrase-"+c.name)
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			got, err := readPassphrase(path)
			if err != nil {
				t.Fatalf("readPassphrase: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("readPassphrase(%q): got %q want %q", c.content, got, c.want)
			}
		})
	}
}
