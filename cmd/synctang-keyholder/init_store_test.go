// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain keeps every test off the real keyring unless it asks for one: the probe that init uses
// to choose a store says there is none, and a test that wants one installs the mock.
func TestMain(m *testing.M) {
	keyringAvailable = func() error { return errors.New("no keyring in tests") }
	os.Exit(m.Run())
}

func withMockKeyring(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	was := keyringAvailable
	keyringAvailable = func() error { return nil }
	t.Cleanup(func() { keyringAvailable = was })
}

func TestInitKeepsTheKeyInTheKeyringWhenThereIsOne(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	code, out, errOut := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Errorf("a key file was made although the keyring was there: %v", err)
	}
	if spec, ok, _ := readStoreRecord(keyFile); !ok || spec != "secret-service" {
		t.Errorf("record = %q, %v", spec, ok)
	}
	if !strings.Contains(out, "held by secret-service") || !strings.Contains(out, "keyring") {
		t.Errorf("init should say where the key went and what that means:\n%s", out)
	}
	// and the rest of the program finds it there
	if code, out, e := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 || !strings.Contains(out, "S: ") {
		t.Fatalf("export-pubkey: exit %d:\n%s%s", code, out, e)
	}
	if code, out, _ := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 || !strings.Contains(out, "held by secret-service") || !strings.Contains(out, "readable: yes") {
		t.Errorf("status:\n%s", out)
	}
}

func TestInitFallsBackToAFileWithAWarningWhenThereIsNoKeyring(t *testing.T) {
	keyFile, transportFile := keyholderDir(t) // the test setup says there is no keyring
	code, out, errOut := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("no key file: %v", err)
	}
	all := out + errOut
	for _, want := range []string{"no keyring", "plain file"} {
		if !strings.Contains(all, want) {
			t.Errorf("the fallback should be said, with its reason; lacks %q:\n%s", want, all)
		}
	}
}

func TestInitOnARequestedStoreThatIsNotThereFailsInsteadOfFallingBack(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	code, _, errOut := runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 1 || !strings.Contains(errOut, "no keyring in tests") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("asking for the keyring and getting a file would be a silent downgrade")
	}
}

func TestInitCanBeToldToUseAFile(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("no key file: %v", err)
	}
}

func TestInitRefusesAnUnknownStoreAndNamesTheRealOnes(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	code, _, errOut := runKH("init", "--store", "quantum-vault", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 2 || !strings.Contains(errOut, "quantum-vault") || !strings.Contains(errOut, "secret-service") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestInitDoesNotReplaceAKeyThatAnotherStoreHolds(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	code, _, errOut := runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code == 0 || !strings.Contains(errOut, "--force") {
		t.Fatalf("a second init, into another store, would orphan the first key: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("the refused init still wrote a file")
	}
}

func TestInitForceReplacesTheKeyAndLeavesNoCopyInTheOldStore(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	oldPub := pubOf(t, keyFile, transportFile)
	if code, _, e := runKH("init", "--force", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	if pubOf(t, keyFile, transportFile) == oldPub {
		t.Error("--force kept the old key")
	}
	if ok, _ := newSecretServiceStore(keyFile).Exists(); ok {
		t.Error("the old key is still in the keyring after being replaced")
	}
	if spec, _, _ := readStoreRecord(keyFile); spec != "file" {
		t.Errorf("record = %q after the key moved to a file", spec)
	}
}

func pubOf(t *testing.T, keyFile, transportFile string) string {
	t.Helper()
	code, out, e := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatal(e)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "S: ") {
			return l
		}
	}
	t.Fatal("no S line")
	return ""
}
