// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

func TestRunInitThenExportPubkey(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	transportKeyFile := filepath.Join(dir, "transport.pem")

	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "--key-file", keyFile, "--transport-key-file", transportKeyFile},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run init: exit %d, stderr %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"export-pubkey", "--key-file", keyFile, "--transport-key-file", transportKeyFile},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run export-pubkey: exit %d, stderr %q", code, stderr.String())
	}

	var pubHex, transportID string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if rest, ok := strings.CutPrefix(line, "S: "); ok {
			pubHex = rest
		}
		if rest, ok := strings.CutPrefix(line, "transport-id: "); ok {
			transportID = rest
		}
	}
	if pubHex == "" {
		t.Fatalf("export-pubkey did not print an \"S: \" line: %q", stdout.String())
	}
	if transportID == "" {
		t.Fatalf("export-pubkey did not print a \"transport-id: \" line: %q", stdout.String())
	}

	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("export-pubkey did not print hex: %q: %v", pubHex, err)
	}
	if len(pub) != 65 {
		t.Fatalf("exported public key length: got %d want 65 (uncompressed P-256 point)", len(pub))
	}

	s, err := loadPrivateKey(keyFile)
	if err != nil {
		t.Fatalf("loadPrivateKey: %v", err)
	}
	want, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	if !bytes.Equal(pub, want) {
		t.Fatalf("exported public key does not match the saved key:\n got=%x\n want=%x", pub, want)
	}
}

func TestRunInitRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	transportKeyFile := filepath.Join(dir, "transport.pem")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--key-file", keyFile, "--transport-key-file", transportKeyFile},
		strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("first run init: exit %d, stderr %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"init", "--key-file", keyFile, "--transport-key-file", transportKeyFile},
		strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit overwriting an existing key without --force")
	}
	if stderr.Len() == 0 {
		t.Fatal("expected an error message on stderr")
	}
}

func TestRunExportPubkeyMissingKey(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"export-pubkey", "--key-file", filepath.Join(dir, "no-such-key"),
		"--transport-key-file", filepath.Join(dir, "transport.pem")},
		strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit for a missing key file")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"frobnicate"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit for an unknown command")
	}
}

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a nonzero exit with no command given")
	}
}
