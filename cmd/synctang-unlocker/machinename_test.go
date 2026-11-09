// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

func fakeHostname(t *testing.T, name string, err error) {
	t.Helper()
	was := hostname
	t.Cleanup(func() { hostname = was })
	hostname = func() (string, error) { return name, err }
}

func TestMachineNameIsTheOneGivenThenTheVolumesThenTheHostname(t *testing.T) {
	for _, tc := range []struct {
		given, existing, host string
		hostErr               error
		want                  string
	}{
		{"mine", "vault", "study-box", nil, "mine"},
		{"", "vault", "study-box", nil, "vault"},
		{"", "", "study-box", nil, "study-box"},
		{"", "", "  study-box\n", nil, "study-box"},
		{"", "", "localhost", nil, ""},
		{"", "", "localhost.localdomain", nil, ""},
		{"", "", "(none)", nil, ""},
		{"", "", "", nil, ""},
		{"", "", "", errors.New("no hostname"), ""},
	} {
		fakeHostname(t, tc.host, tc.hostErr)
		if got := resolveMachineName(tc.given, tc.existing); got != tc.want {
			t.Errorf("given %q, existing %q, hostname %q (%v): %q, want %q", tc.given, tc.existing, tc.host, tc.hostErr, got, tc.want)
		}
	}
}

func TestExistingMachineNameIsReadFromTheVolumesToken(t *testing.T) {
	pass := []byte("name-test-passphrase")
	fresh := formatLoopbackImage(t, pass)
	if got := existingMachineName(fresh); got != "" {
		t.Errorf("a volume with no token has no name, got %q", got)
	}
	enrolled := formatLoopbackImage(t, pass)
	s, _ := mrcore.P256().RandomScalar()
	S, _ := mrcore.P256().ScalarBaseMult(s)
	if err := enrolRecipient(enrolled, pass, S, "vault", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	if got := existingMachineName(enrolled); got != "vault" {
		t.Errorf("name = %q, want vault", got)
	}
	if got := existingMachineName(filepath.Join(t.TempDir(), "missing.img")); got != "" {
		t.Errorf("an unreadable device has no name, got %q", got)
	}
}

// pairOutput runs "pair" for two seconds against an image, on a direct address so that it
// touches no relay, and returns what it printed before its window closed.
func pairOutput(t *testing.T, device string, pass []byte, extra ...string) string {
	t.Helper()
	dir := t.TempDir()
	ppFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(ppFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"pair", "--device", device, "--existing-passphrase-file", ppFile,
		"--machine-key-file", filepath.Join(dir, "machine.pem"), "--direct-addr", freePortForTest(t),
		"--timeout", "2s", "--qr", "plain"}, extra...)
	var out, errOut bytes.Buffer
	run(args, &out, &errOut)
	return out.String()
}

func TestPairShowsTheHostnameWhenNoNameIsGiven(t *testing.T) {
	fakeHostname(t, "study-box", nil)
	pass := []byte("name-test-passphrase")
	out := pairOutput(t, formatLoopbackImage(t, pass), pass)
	if !strings.Contains(out, "name=study-box") || !strings.Contains(out, "pair it with study-box") {
		t.Errorf("the link and the instructions should carry the hostname:\n%s", out)
	}
}

func TestPairKeepsTheNameTheVolumeAlreadyHas(t *testing.T) {
	fakeHostname(t, "study-box", nil)
	pass := []byte("name-test-passphrase")
	device := formatLoopbackImage(t, pass)
	s, _ := mrcore.P256().RandomScalar()
	S, _ := mrcore.P256().ScalarBaseMult(s)
	if err := enrolRecipient(device, pass, S, "vault", "MACHINE-ID", ""); err != nil {
		t.Fatal(err)
	}
	out := pairOutput(t, device, pass)
	if !strings.Contains(out, "name=vault") || strings.Contains(out, "study-box") {
		t.Errorf("a volume that already has a name keeps it:\n%s", out)
	}
}

func TestPairPrefersTheNameItIsGiven(t *testing.T) {
	fakeHostname(t, "study-box", nil)
	pass := []byte("name-test-passphrase")
	out := pairOutput(t, formatLoopbackImage(t, pass), pass, "--name", "the-server")
	if !strings.Contains(out, "name=the-server") || strings.Contains(out, "study-box") {
		t.Errorf("--name wins:\n%s", out)
	}
}

func TestEnrolRecordsTheHostnameInANewToken(t *testing.T) {
	fakeHostname(t, "study-box", nil)
	pass := []byte("name-test-passphrase")
	device := formatLoopbackImage(t, pass)
	dir := t.TempDir()
	ppFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(ppFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := mrcore.P256().RandomScalar()
	S, _ := mrcore.P256().ScalarBaseMult(s)
	var out, errOut bytes.Buffer
	code := run([]string{"enrol", "--device", device, "--pubkey", hex.EncodeToString(S), "--recipient-transport-id", testKH1,
		"--existing-passphrase-file", ppFile, "--machine-key-file", filepath.Join(dir, "machine.pem")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got := existingMachineName(device); got != "study-box" {
		t.Errorf("the token's machine name = %q, want study-box", got)
	}
}
