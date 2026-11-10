// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// A store hands back whatever it holds. A key that is well formed (32 bytes of hex) and not this key holder's
// own is the case no check of form can refute: another program wrote the item, the item belongs to another
// key file, a backup was restored over it. What refutes it is the public key recorded when the key was made.

func hexOf32(b byte) string { return hex.EncodeToString(scalar32(b)) }

func TestAKeyThatIsNotTheOneThatWasMadeIsRefusedInTheKeyring(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	made := pubOf(t, keyFile, transportFile)

	// Another program, or another key file's item, replaces what the keyring holds with a valid scalar.
	if err := keyring.Set(secretServiceName, newSecretServiceStore(keyFile).account, hexOf32(0x42)); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile},
		{"unlock", "--yes", "--timeout", "1s", "--direct-addr", "127.0.0.1:1", "--key-file", keyFile, "--transport-key-file", transportFile, machineIDA},
	} {
		code, out, errOut := runKH(args...)
		if code == 0 || !strings.Contains(errOut, "is not the key this key holder was made with") {
			t.Errorf("%v: exit %d:\n%s%s", args[0], code, out, errOut)
		}
		if strings.Contains(out, made[len("S: "):]) {
			t.Errorf("%v printed the public key of the key that was made, from a key that is not it", args[0])
		}
	}
	// And it is not dialled with: unlock stopped before anything was sent anywhere.
	_, _, errOut := runKH("unlock", "--yes", "--timeout", "1s", "--direct-addr", "127.0.0.1:1", "--key-file", keyFile, "--transport-key-file", transportFile, machineIDA)
	if strings.Contains(errOut, "dialing") {
		t.Errorf("it dialled the machine with the wrong key:\n%s", errOut)
	}
}

func TestStatusSaysTheKeyIsNotTheOneThatWasMade(t *testing.T) {
	withMockKeyring(t)
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile)
	code, out, _ := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 || !strings.Contains(out, "readable: yes") || !strings.Contains(out, "matches the public key recorded") {
		t.Fatalf("a key that is the one made:\n%s", out)
	}
	_ = keyring.Set(secretServiceName, newSecretServiceStore(keyFile).account, hexOf32(0x42))
	code, out, _ = runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 || !strings.Contains(out, "readable: no") || !strings.Contains(out, "is not the key this key holder was made with") {
		t.Errorf("a key that is not:\n%s", out)
	}
}

func TestAKeyThatIsNotTheOneThatWasMadeIsRefusedInAFile(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if err := os.WriteFile(keyFile, []byte(hexOf32(0x42)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code == 0 || !strings.Contains(errOut, "is not the key this key holder was made with") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestAKeyThatIsNotTheOneThatWasMadeIsRefusedInASealedBlob(t *testing.T) {
	(&fakeCreds{}).install(t)
	was := credsAvailable
	t.Cleanup(func() { credsAvailable = was })
	credsAvailable = func(bool) error { return nil }
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "systemd-creds", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	// Another valid credential is put where this one was.
	other := newCredStore(keyFile+".cred", false)
	if err := other.Save(scalar32(0x42), true); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code == 0 || !strings.Contains(errOut, "is not the key this key holder was made with") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// What is not even a key is refused too, from every store, in words that say what is wrong with it.
func TestWhatIsNotAKeyIsRefusedFromTheKeyring(t *testing.T) {
	withMockKeyring(t)
	for name, item := range map[string]string{
		"not hex":        "this is not a key",
		"too short":      hex.EncodeToString(scalar32(1)[:16]),
		"too long":       hexOf32(1) + "00",
		"empty":          "",
		"hex, two lines": hexOf32(1) + "\n" + hexOf32(2),
	} {
		keyFile, transportFile := keyholderDir(t)
		_, _, _ = runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile)
		if err := keyring.Set(secretServiceName, newSecretServiceStore(keyFile).account, item); err != nil {
			t.Fatal(err)
		}
		code, _, errOut := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
		if code == 0 || !(strings.Contains(errOut, "valid hex scalar") || strings.Contains(errOut, "-byte scalar")) {
			t.Errorf("%s: exit %d: %s", name, code, errOut)
		}
	}
}

// A key made before the public key was recorded has nothing to be checked against, and is used as it was:
// nothing is invented to refuse it with.
func TestAKeyWithNoRecordedPublicKeyIsUsedAsItIs(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	if err := os.WriteFile(storeRecordPath(keyFile), []byte("file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Errorf("exit %d: %s", code, e)
	}
	_, out, _ := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(out, "no public key was recorded") {
		t.Errorf("status should say there is nothing to compare it with:\n%s", out)
	}
}

func TestTheRecordHoldsTheStoreAndThePublicKeyAndNoSecret(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	_, _, _ = runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile)
	raw, err := os.ReadFile(storeRecordPath(keyFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	pub := pubOf(t, keyFile, transportFile)[len("S: "):]
	if len(lines) != 2 || lines[0] != "file" || lines[1] != "public "+pub {
		t.Errorf("record:\n%s", raw)
	}
	secret, _ := os.ReadFile(keyFile)
	if strings.Contains(string(raw), strings.TrimSpace(string(secret))) {
		t.Error("the record holds the secret")
	}
}
