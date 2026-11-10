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
		"--recipient-transport-id", testCLIRecipID,
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
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != testCLIRecipID {
		t.Fatalf("recipient transport id = (%q, %v), want (%q, true)", id, ok, testCLIRecipID)
	}
}

// A8, exercised at the CLI level: "revoke" revokes exactly the
// named recipient and leaves the other one enrolled.
func TestRunRevokeEndToEnd(t *testing.T) {
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
		{S1, testKH1},
		{S2, testKH2},
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
		"revoke",
		"--device", device,
		"--existing-passphrase-file", passphraseFile,
		testKH1,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run revoke: exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "revoked recipient "+testKH1) {
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
		t.Fatalf("expected 1 recipient after revoke, got %d", len(tok.Recipients))
	}
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != testKH2 {
		t.Fatalf("surviving recipient = (%q, %v), want (%q, true)", id, ok, testKH2)
	}
}

// "rotate" must replace the named key holder's key in one command,
// keeping its transport id: a rotation that lost it would leave an
// entry the agent cannot restrict to any peer.
func TestRunRotateEndToEnd(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	passphraseFile := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(passphraseFile, existingPassphrase, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	g := mrcore.P256()
	sOld := mustScalar(t)
	SOld, err := g.ScalarBaseMult(sOld)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	sNew := mustScalar(t)
	SNew, err := g.ScalarBaseMult(sNew)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"enrol",
		"--device", device,
		"--pubkey", hex.EncodeToString(SOld),
		"--existing-passphrase-file", passphraseFile,
		"--recipient-transport-id", testKH1,
		"--machine-key-file", filepath.Join(t.TempDir(), "machine.pem"),
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run enrol: exit %d, stderr %q", code, stderr.String())
	}

	pOld := recoverPForTest(t, g, exportTokenForTest(t, device).Recipients[0], sOld)

	stdout.Reset()
	stderr.Reset()
	code := run([]string{
		"rotate",
		"--device", device,
		"--pubkey", hex.EncodeToString(SNew),
		"--existing-passphrase-file", passphraseFile,
		testKH1,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run rotate: exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rotated recipient "+testKH1) {
		t.Errorf("expected confirmation of the rotation, got stdout %q", stdout.String())
	}

	tok := exportTokenForTest(t, device)
	if len(tok.Recipients) != 1 {
		t.Fatalf("expected 1 recipient after rotate, got %d", len(tok.Recipients))
	}
	if id, ok := tok.Recipients[0].TransportDeviceID(); !ok || id != testKH1 {
		t.Fatalf("rotated recipient = (%q, %v), want (%q, true)", id, ok, testKH1)
	}
	if opensDeviceForTest(t, device, pOld) {
		t.Error("the rotated-away key still opens the device")
	}
	if !opensDeviceForTest(t, device, recoverPForTest(t, g, tok.Recipients[0], sNew)) {
		t.Error("the new key does not open the device")
	}
}

// What revoke and rotate want is said plainly, and the options that enrol used to take for them are gone:
// there is one way to do each thing.
func TestRevokeAndRotateWantWhatTheyNeed(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"revoke", "--device", "/nonexistent", "--existing-passphrase-file", "/nonexistent"}, "transport id or --keyslot"},
		{[]string{"revoke", "--existing-passphrase-file", "/nonexistent", testKH1}, "--device"},
		{[]string{"revoke", "--device", "/nonexistent", testKH1}, "--existing-passphrase-file"},
		{[]string{"revoke", "--device", "/nonexistent", "--existing-passphrase-file", "/nonexistent", testKH1, testKH2}, "one key holder"},
		{[]string{"rotate", "--device", "/nonexistent", "--existing-passphrase-file", "/nonexistent", testKH1}, "--pubkey"},
		{[]string{"rotate", "--device", "/nonexistent", "--existing-passphrase-file", "/nonexistent", "--pubkey", "04aa"}, "transport id"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tc.args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want it to mention %q)", tc.args, code, stderr.String(), tc.want)
		}
	}
}

func TestEnrolNoLongerRemovesOrReplaces(t *testing.T) {
	for _, flag := range []string{"--remove", "--replace", "--remove-keyslot", "--keyslot"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"enrol", "--device", "/nonexistent", flag, "x"}, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "unrecognised option '"+flag+"'") {
			t.Errorf("%s: exit %d, stderr %q", flag, code, stderr.String())
		}
	}
}

// A transport id and the options may come in either order, as with other GNU programs.
func TestRevokeTakesTheTransportIDBeforeOrAfterTheOptions(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	for _, before := range []bool{true, false} {
		device := formatLoopbackImage(t, pass)
		if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH1); err != nil {
			t.Fatal(err)
		}
		passFile := filepath.Join(t.TempDir(), "pass")
		if err := os.WriteFile(passFile, pass, 0o600); err != nil {
			t.Fatal(err)
		}
		args := []string{"revoke", "--device", device, "--existing-passphrase-file", passFile}
		if before {
			args = []string{"revoke", testKH1, "--device", device, "--existing-passphrase-file", passFile}
		} else {
			args = append(args, testKH1)
		}
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("before=%v: exit %d: %s", before, code, stderr.String())
		}
		if got := len(exportTokenForTest(t, device).Recipients); got != 0 {
			t.Errorf("before=%v: %d key holders left", before, got)
		}
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

// Enrolling a disk image belonging to some other machine is a
// supported use (--device takes an image as well as a block device),
// and in that case --machine-key-file's default is wrong: it points at
// this host's own identity, which for an image is nobody's. enrol then
// silently generates a brand new identity here and prints it as "this
// machine's transport id", so a key holder paired against that id waits
// for a machine that announces a different one, with nothing anywhere
// to explain it. Hit exactly that way while enrolling a phone onto the
// test VM's image.
//
// enrol cannot know which case it is in, so it says so when it creates
// an identity rather than loading one.
func TestRunEnrolWarnsWhenItCreatesAMachineIdentity(t *testing.T) {
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

	keyFile := filepath.Join(t.TempDir(), "machine.pem")
	enrol := func() (string, string, int) {
		var stdout, stderr bytes.Buffer
		code := run([]string{
			"enrol",
			"--device", device,
			"--pubkey", hex.EncodeToString(S),
			"--recipient-transport-id", testKH1,
			"--existing-passphrase-file", passphraseFile,
			"--name", "cli-test-machine",
			"--machine-key-file", keyFile,
		}, &stdout, &stderr)
		return stdout.String(), stderr.String(), code
	}

	_, stderr, code := enrol()
	if code != 0 {
		t.Fatalf("first enrol: exit %d, stderr %q", code, stderr)
	}
	// A path the caller named explicitly, as here, is not a mistake:
	// creating an identity there is what they asked for and the
	// printed id is the enrolled device's. Warning anyway is a false
	// alarm, which is what this did the first time it fired for real.
	if strings.Contains(stderr, "--machine-key-file") {
		t.Errorf("an explicitly named key file should not be warned about, got stderr %q", stderr)
	}

	// Second run loads the identity it just wrote, which is the
	// ordinary case and must stay quiet: a warning printed every time
	// is a warning nobody reads.
	_, stderr, code = enrol()
	if code != 0 {
		t.Fatalf("second enrol: exit %d, stderr %q", code, stderr)
	}
	if strings.Contains(stderr, "--machine-key-file") {
		t.Errorf("loading an existing identity should be quiet, got stderr %q", stderr)
	}
}

// The footgun itself, which cannot be exercised through the CLI
// without writing to the real /etc: enrolling a disk image for another
// machine, without saying where that machine's identity lives, prints
// this host's id as though it were the machine's.
func TestWarnsOnlyWhenTheIdentityPathWasNotChosen(t *testing.T) {
	cases := []struct {
		name    string
		isNew   bool
		keyFile string
		want    bool
	}{
		{"created at the default path", true, defaultMachineKeyFile, true},
		{"created where the caller asked", true, "/tmp/some-image/etc/synctang/machine.pem", false},
		{"loaded an existing default", false, defaultMachineKeyFile, false},
		{"loaded an existing explicit one", false, "/tmp/some-image/etc/synctang/machine.pem", false},
	}
	for _, c := range cases {
		if got := shouldWarnAboutNewIdentity(c.isNew, c.keyFile); got != c.want {
			t.Errorf("%s: shouldWarnAboutNewIdentity = %v, want %v", c.name, got, c.want)
		}
	}
}

// "sudo cmd --existing-passphrase-file <(printf %s ...)" fails because sudo closes
// every file descriptor but the standard three before it runs the command, so the
// /dev/fd/N that bash made for the process substitution is gone. The bare "no such
// file or directory" sent someone to look for a file; it has to say what happened
// and what to do instead.
func TestAPassphraseFileThatIsAClosedDescriptorSaysWhySudoDidThat(t *testing.T) {
	_, err := readPassphrase("/dev/fd/9999")
	if err == nil {
		t.Fatal("a descriptor that is not open was read")
	}
	for _, want := range []string{"sudo", "root shell", "--existing-passphrase-file -"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestAnOrdinaryMissingPassphraseFileIsNotBlamedOnSudo(t *testing.T) {
	_, err := readPassphrase(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing file was read")
	}
	if strings.Contains(err.Error(), "sudo") {
		t.Errorf("a plain missing file is explained by sudo: %v", err)
	}
}

func TestVersionFlagPrintsTheVersionAndSucceeds(t *testing.T) {
	for _, flag := range []string{"--version", "-version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{flag}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", flag, code, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "synctang-unlocker ") || stderr.Len() != 0 {
			t.Errorf("%s: stdout %q stderr %q", flag, stdout.String(), stderr.String())
		}
	}
}

func TestTheUsageMentionsTheVersionFlag(t *testing.T) {
	if !strings.Contains(usage, "synctang-unlocker --version") {
		t.Errorf("usage does not mention --version:\n%s", usage)
	}
}

func TestAMisspeltCommandSuggestsTheOneMeant(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"stauts"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `did you mean "status"?`) {
		t.Errorf("exit %d: %s", code, stderr.String())
	}
}
