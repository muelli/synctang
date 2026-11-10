// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"strings"
	"testing"
)

// "Where is my key?" is answered by status, and by --verbose (or SYNCTANG_DEBUG=1) on any command: a log of
// what was read from where, which says nothing of the key itself.

func TestVerboseSaysWhereTheKeyIsReadFromAndThatItMatches(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	secret, _ := os.ReadFile(keyFile)

	code, out, errOut := runKH("status", "--verbose", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"synctang-keyholder: debug: ",
		"names the store \"file\"",
		"read the key from file (file " + keyFile + ")",
		"its public key matches the one recorded",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("no %q in:\n%s", want, errOut)
		}
	}
	if strings.Contains(out+errOut, strings.TrimSpace(string(secret))) {
		t.Error("the secret is in the output")
	}
}

func TestVerboseNamesTheKeyringItem(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile)
	_, _, errOut := runKH("export-pubkey", "--verbose", "--key-file", keyFile, "--transport-key-file", transportFile)
	want := "read the key from secret-service (Secret Service item \"synctang-keyholder\" for " + keyFile + ")"
	if !strings.Contains(errOut, want) {
		t.Errorf("no %q in:\n%s", want, errOut)
	}
}

func TestVerboseSaysWhenTheKeyIsNotTheOneThatWasMade(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	_ = os.WriteFile(keyFile, []byte(hexOf32(0x42)+"\n"), 0o600)
	_, _, errOut := runKH("export-pubkey", "--verbose", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(errOut, "debug: read the key from file") || !strings.Contains(errOut, "is not the key this key holder was made with") {
		t.Errorf("%s", errOut)
	}
}

func TestVerboseSaysWhereAKeyWasSavedAndAnUnrecordedKeyIsLookedForInAFile(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, errOut := runKH("init", "--verbose", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(errOut, "saved the new key in file (file "+keyFile+")") {
		t.Errorf("init:\n%s", errOut)
	}
	_ = os.Remove(storeRecordPath(keyFile))
	_, _, errOut = runKH("export-pubkey", "--verbose", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(errOut, "there is no record") || !strings.Contains(errOut, "plain file "+keyFile) {
		t.Errorf("no record:\n%s", errOut)
	}
}

func TestNothingIsLoggedWithoutVerbose(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	_, _, errOut := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if strings.Contains(errOut, "debug:") {
		t.Errorf("a debug line without --verbose:\n%s", errOut)
	}
}

func TestTheEnvironmentAsksForTheDebugLogToo(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	t.Setenv("SYNCTANG_DEBUG", "1")
	_, _, errOut := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(errOut, "debug: read the key from file") {
		t.Errorf("SYNCTANG_DEBUG=1 did not turn it on:\n%s", errOut)
	}
}
