// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCreds replaces systemd-creds: "encrypting" prefixes the input, "decrypting" removes it, and
// every call is recorded. A nil error means the tool worked.
type fakeCreds struct {
	calls [][]string
	stdin [][]byte
	err   error
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

func (f *fakeCreds) install(t *testing.T) {
	t.Helper()
	was := credsRun
	t.Cleanup(func() { credsRun = was })
	credsRun = func(stdin []byte, args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		f.stdin = append(f.stdin, append([]byte(nil), stdin...))
		if f.err != nil {
			return nil, f.err
		}
		switch args[0] {
		case "encrypt":
			// Reversed, so that what is on disk is not the hex scalar and a test can tell.
			return append([]byte("CRED:"), reversed(stdin)...), nil
		case "decrypt":
			return reversed(bytes.TrimPrefix(stdin, []byte("CRED:"))), nil
		}
		return nil, errors.New("unexpected " + args[0])
	}
}

func TestCredStoreSavesAnEncryptedBlobAndLoadsTheScalarBack(t *testing.T) {
	f := &fakeCreds{}
	f.install(t)
	blob := filepath.Join(t.TempDir(), "key.cred")
	st := newCredStore(blob, false)
	if err := st.Save(scalar32(6), false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(blob)
	if err != nil || !bytes.HasPrefix(raw, []byte("CRED:")) {
		t.Fatalf("the blob should be what systemd-creds wrote: %q %v", raw, err)
	}
	if bytes.Contains(raw, []byte(hex.EncodeToString(scalar32(6)))) {
		t.Fatal("the scalar must not be on disk in the clear")
	}
	if fi, _ := os.Stat(blob); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	got, err := st.Load()
	if err != nil || !bytes.Equal(got, scalar32(6)) {
		t.Fatalf("%x %v", got, err)
	}
	if ok, _ := st.Exists(); !ok {
		t.Error("Exists")
	}
}

func TestCredStoreAsksSystemdForTheUsersOwnScopeAndNamesTheCredential(t *testing.T) {
	f := &fakeCreds{}
	f.install(t)
	st := newCredStore(filepath.Join(t.TempDir(), "key.cred"), false)
	_ = st.Save(scalar32(6), false)
	_, _ = st.Load()
	for i, call := range f.calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "--user") || !strings.Contains(joined, "--name=synctang-keyholder") {
			t.Errorf("call %d = %q: it must use the user's scope (system scope is refused to ordinary users) and a fixed name", i, joined)
		}
		if strings.Contains(joined, "tpm2") {
			t.Errorf("call %d = %q: the plain variant does not ask for the TPM", i, joined)
		}
	}
	if string(f.stdin[0]) != hex.EncodeToString(scalar32(6)) {
		t.Errorf("the scalar goes to systemd-creds on stdin, as hex: %q", f.stdin[0])
	}
}

func TestTheTPMVariantAsksForTheTPM(t *testing.T) {
	f := &fakeCreds{}
	f.install(t)
	st := newCredStore(filepath.Join(t.TempDir(), "key.cred"), true)
	if st.Name() != "systemd-creds:tpm2" {
		t.Errorf("name %q", st.Name())
	}
	_ = st.Save(scalar32(6), false)
	// systemd refuses a bare tpm2 key in user scope; host+tpm2 is the one that is both user-scoped
	// and bound to the TPM.
	if !strings.Contains(strings.Join(f.calls[0], " "), "--with-key=host+tpm2") {
		t.Errorf("encrypt call = %v", f.calls[0])
	}
	for _, a := range f.calls[0] {
		if a == "--with-key=tpm2" {
			t.Errorf("a bare tpm2 key is refused in user scope: %v", f.calls[0])
		}
	}
}

func TestTheTPMVariantIsNotOfferedWhereThereIsNoTPM(t *testing.T) {
	(&fakeCreds{}).install(t)
	was := tpmPresent
	t.Cleanup(func() { tpmPresent = was })
	tpmPresent = func() error { return errors.New("no TPM") }
	err := systemdCredsAvailable(true)
	if err == nil || !strings.Contains(err.Error(), "no TPM") {
		t.Fatalf("systemd-creds quietly falls back to a key without the TPM, so the store must check first: %v", err)
	}
	if err := systemdCredsAvailable(false); err != nil {
		t.Errorf("the plain variant needs no TPM: %v", err)
	}
}

func TestTheTwoVariantsKeepTheirBlobsApart(t *testing.T) {
	a, _ := storeFromSpec("systemd-creds", "/k/key")
	b, _ := storeFromSpec("systemd-creds:tpm2", "/k/key")
	if a.(*credStore).blob == b.(*credStore).blob {
		t.Fatal("one blob for both variants: replacing one with the other would delete the new key")
	}
}

func TestInitForceFromOneCredVariantToTheOtherKeepsTheNewKey(t *testing.T) {
	(&fakeCreds{}).install(t)
	was, wasTPM := credsAvailable, tpmPresent
	t.Cleanup(func() { credsAvailable, tpmPresent = was, wasTPM })
	credsAvailable = func(bool) error { return nil }
	keyFile, transportFile := keyholderDir(t)
	for _, args := range [][]string{{"--store", "systemd-creds"}, {"--force", "--store", "systemd-creds:tpm2"}} {
		args = append(args, "--key-file", keyFile, "--transport-key-file", transportFile)
		if code, out, e := runKH(append([]string{"init"}, args...)...); code != 0 {
			t.Fatalf("exit %d: %s%s", code, out, e)
		}
	}
	if code, out, e := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 || !strings.Contains(out, "readable: yes") {
		t.Fatalf("the new key is not readable (exit %d):\n%s%s", code, out, e)
	}
}

func TestCredStoreRefusesToOverwriteUnlessAsked(t *testing.T) {
	(&fakeCreds{}).install(t)
	st := newCredStore(filepath.Join(t.TempDir(), "key.cred"), false)
	_ = st.Save(scalar32(1), false)
	if err := st.Save(scalar32(2), false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := st.Load(); !bytes.Equal(got, scalar32(1)) {
		t.Fatal("a refused save changed the key")
	}
	if err := st.Save(scalar32(2), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(); !bytes.Equal(got, scalar32(2)) {
		t.Fatal("--force did not replace the key")
	}
}

func TestCredStoreSaysThereIsNoKeyAndDeletes(t *testing.T) {
	(&fakeCreds{}).install(t)
	blob := filepath.Join(t.TempDir(), "key.cred")
	st := newCredStore(blob, false)
	if _, err := st.Load(); !errors.Is(err, errNoKey) {
		t.Fatalf("err = %v", err)
	}
	_ = st.Save(scalar32(1), false)
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Error("the blob is still there")
	}
	if err := st.Delete(); err != nil {
		t.Errorf("deleting what is not there: %v", err)
	}
}

func TestACredStoreThatCannotRunIsNotMistakenForNoKey(t *testing.T) {
	f := &fakeCreds{}
	f.install(t)
	st := newCredStore(filepath.Join(t.TempDir(), "key.cred"), false)
	_ = st.Save(scalar32(1), false)
	f.err = errors.New("Failed to decrypt: No such device")
	_, err := st.Load()
	if err == nil || errors.Is(err, errNoKey) || !strings.Contains(err.Error(), "systemd-creds") || !strings.Contains(err.Error(), "256") {
		t.Fatalf("err = %v: the tool failing is not an absent key, and the error should say what the store needs", err)
	}
}

func TestCredStoreDescribesWhatItDoesAndDoesNotProtect(t *testing.T) {
	plain := newCredStore("/x/key.cred", false).Describe()
	for _, want := range []string{"systemd", "same disk", "root", "software running as you", "256"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the plain variant's description lacks %q: %s", want, plain)
		}
	}
	tpm := newCredStore("/x/key.cred", true).Describe()
	for _, want := range []string{"TPM", "cannot be moved", "backup"} {
		if !strings.Contains(tpm, want) {
			t.Errorf("the TPM variant's description lacks %q: %s", want, tpm)
		}
	}
	for _, d := range []string{plain, tpm} {
		if strings.Contains(strings.ToLower(d), "secure") {
			t.Errorf("a store is never called secure: %s", d)
		}
	}
}

func TestARecordCanNameTheCredStores(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	for spec, want := range map[string]string{"systemd-creds": "systemd-creds", "systemd-creds:tpm2": "systemd-creds:tpm2"} {
		_ = writeStoreRecord(keyFile, spec, "")
		st, err := openStore(keyFile)
		if err != nil || st.Name() != want {
			t.Errorf("%s: %v, %v", spec, st, err)
		}
	}
	if st, _ := storeFromSpec("systemd-creds", keyFile); st.(*credStore).blob != keyFile+".cred" {
		t.Errorf("the blob lives beside the key file")
	}
}

func TestInitCanUseSystemdCredsButAutoNeverPicksIt(t *testing.T) {
	(&fakeCreds{}).install(t)
	was := credsAvailable
	t.Cleanup(func() { credsAvailable = was })
	credsAvailable = func(bool) error { return nil }

	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	if spec, _, _ := readStoreRecord(keyFile); spec == "systemd-creds" {
		t.Error("auto must not pick systemd-creds: it is for those who ask")
	}

	keyFile2, transportFile2 := keyholderDir(t)
	code, out, e := runKH("init", "--store", "systemd-creds", "--key-file", keyFile2, "--transport-key-file", transportFile2)
	if code != 0 || !strings.Contains(out, "held by systemd-creds") {
		t.Fatalf("exit %d:\n%s%s", code, out, e)
	}
	if _, err := os.Stat(keyFile2); !os.IsNotExist(err) {
		t.Error("a plain key file was made")
	}
	if _, err := os.Stat(keyFile2 + ".cred"); err != nil {
		t.Errorf("no credential blob: %v", err)
	}
	if code, out, _ := runKH("export-pubkey", "--key-file", keyFile2, "--transport-key-file", transportFile2); code != 0 || !strings.Contains(out, "S: ") {
		t.Errorf("export-pubkey: %d %s", code, out)
	}
}

func TestAskingForSystemdCredsWhereItCannotWorkFails(t *testing.T) {
	was := credsAvailable
	t.Cleanup(func() { credsAvailable = was })
	credsAvailable = func(tpm bool) error {
		return errors.New("Failed to encrypt: io.systemd.InteractiveAuthenticationRequired")
	}
	keyFile, transportFile := keyholderDir(t)
	code, _, errOut := runKH("init", "--store", "systemd-creds:tpm2", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 1 || !strings.Contains(errOut, "InteractiveAuthenticationRequired") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("asking for systemd-creds and getting a file would be a silent downgrade")
	}
}

// Against the real tool, where there is one that works for this user (systemd 256 or later, in a
// session): the round trip that the fake only imitates.
func TestCredStoreWithTheRealSystemdCreds(t *testing.T) {
	if _, err := exec.LookPath("systemd-creds"); err != nil {
		t.Skip("no systemd-creds")
	}
	if err := systemdCredsAvailable(false); err != nil {
		t.Skipf("systemd-creds does not work for this user here: %v", err)
	}
	blob := filepath.Join(t.TempDir(), "key.cred")
	st := newCredStore(blob, false)
	if err := st.Save(scalar32(0x3c), false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(blob)
	if bytes.Contains(raw, []byte(hex.EncodeToString(scalar32(0x3c)))) {
		t.Fatal("the scalar is in the blob in the clear")
	}
	got, err := st.Load()
	if err != nil || !bytes.Equal(got, scalar32(0x3c)) {
		t.Fatalf("%x %v", got, err)
	}
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
}

// A blob that is not this store's (another name, not a credential at all) is not a systemd that is
// too old, and saying "need systemd 256" sends the person after the wrong thing.
func TestAWrongBlobIsNotBlamedOnTheSystemdVersion(t *testing.T) {
	blob := filepath.Join(t.TempDir(), "key.cred")
	if err := os.WriteFile(blob, []byte("not a credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, detail := range []string{"Name in credential doesn't match expectations", "Bad credential format"} {
		(&fakeCreds{err: errors.New("systemd-creds: exit status 1: " + detail)}).install(t)
		_, err := newCredStore(blob, false).Load()
		if err == nil || !strings.Contains(err.Error(), detail) {
			t.Fatalf("%s: %v", detail, err)
		}
		if strings.Contains(err.Error(), "systemd 256") || !strings.Contains(err.Error(), "not a credential this store sealed") {
			t.Errorf("%s: %v", detail, err)
		}
	}
	(&fakeCreds{err: errors.New("systemd-creds: exit status 1: Failed to decrypt: io.systemd.InteractiveAuthenticationRequired")}).install(t)
	if _, err := newCredStore(blob, false).Load(); err == nil || !strings.Contains(err.Error(), "systemd 256") {
		t.Errorf("a real scope problem should still say what the store needs: %v", err)
	}
}
