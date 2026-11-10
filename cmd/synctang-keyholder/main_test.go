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

func TestVersionFlagPrintsTheVersionAndSucceeds(t *testing.T) {
	for _, flag := range []string{"--version", "-version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{flag}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", flag, code, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "synctang-keyholder ") || stderr.Len() != 0 {
			t.Errorf("%s: stdout %q stderr %q", flag, stdout.String(), stderr.String())
		}
	}
}

func TestTheUsageMentionsTheVersionFlag(t *testing.T) {
	if !strings.Contains(usage, "synctang-keyholder --version") {
		t.Errorf("usage does not mention --version:\n%s", usage)
	}
}

func TestColourFollowsTheColorFlagAnywhereOnTheCommandLine(t *testing.T) {
	t.Setenv("LANG", "en_GB.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("TERM", "xterm")
	missing := filepath.Join(t.TempDir(), "nokey")
	for _, args := range [][]string{
		{"--color=always", "export-pubkey", "--key-file", missing},
		{"export-pubkey", "--key-file", missing, "--color", "always"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 1 {
			t.Fatalf("%v: exit %d", args, code)
		}
		if !strings.Contains(stderr.String(), "\x1b[31m✗ synctang-keyholder:\x1b[0m") {
			t.Errorf("%v: the error is not red:\n%q", args, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	run([]string{"--color=never", "export-pubkey", "--key-file", missing}, strings.NewReader(""), &stdout, &stderr)
	if strings.Contains(stderr.String(), "\x1b") || !strings.HasPrefix(stderr.String(), "synctang-keyholder: ") {
		t.Errorf("--color=never:\n%q", stderr.String())
	}
	// The default is a terminal's business: a buffer is plain.
	stderr.Reset()
	run([]string{"export-pubkey", "--key-file", missing}, strings.NewReader(""), &stdout, &stderr)
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("colour to a buffer:\n%q", stderr.String())
	}
	if code := run([]string{"--color=sometimes", "status"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Errorf("a bad --color: exit %d", code)
	}
}
