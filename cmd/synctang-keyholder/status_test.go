// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func keyholderDir(t *testing.T) (keyFile, transportFile string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "key"), filepath.Join(dir, "transport.pem")
}

func runKH(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoRecordAndAKeyFileMeansTheFileStore(t *testing.T) {
	keyFile, _ := keyholderDir(t)
	if err := newFileStore(keyFile).Save(scalar32(3), false); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(keyFile)
	if err != nil || st.Name() != "file" {
		t.Fatalf("%v, %v", st, err)
	}
}

func TestNoRecordAndNoKeyIsNoKey(t *testing.T) {
	keyFile, _ := keyholderDir(t)
	if _, err := openStore(keyFile); !errorsIsNoKey(err) {
		t.Fatalf("err = %v, want errNoKey", err)
	}
}

func TestARecordNamesTheStoreThatHoldsTheKey(t *testing.T) {
	keyFile, _ := keyholderDir(t)
	if err := writeStoreRecord(keyFile, "file", ""); err != nil {
		t.Fatal(err)
	}
	spec, ok, err := readStoreRecord(keyFile)
	if err != nil || !ok || spec != "file" {
		t.Fatalf("%q %v %v", spec, ok, err)
	}
	if _, ok, _ := readStoreRecord(filepath.Join(t.TempDir(), "other")); ok {
		t.Error("a key with no record has none")
	}
}

func TestAnUnknownRecordedStoreIsRefusedByName(t *testing.T) {
	keyFile, _ := keyholderDir(t)
	if err := os.WriteFile(keyFile+".store", []byte("quantum-vault\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(keyFile); err == nil || !strings.Contains(err.Error(), "quantum-vault") {
		t.Fatalf("err = %v, want one that names the store", err)
	}
}

func TestStatusSaysWhichStoreHoldsTheKeyAndWhatThatMeans(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	code, out, errOut := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"key: held by file", "plain file", "software running as you", "readable: yes", "transport identity:"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, hexOfScalarIn(t, keyFile)) {
		t.Errorf("status must never print the key:\n%s", out)
	}
}

func TestStatusChangesNothingAndMakesNoIdentity(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	if err := newFileStore(keyFile).Save(scalar32(5), false); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 0 || !strings.Contains(out, "not created yet") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(transportFile); !os.IsNotExist(err) {
		t.Errorf("status created the transport identity: %v", err)
	}
	if _, err := os.Stat(keyFile + ".store"); !os.IsNotExist(err) {
		t.Errorf("status wrote a record: %v", err)
	}
}

func TestStatusWithNoKeySaysSoAndHowToMakeOne(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	code, out, errOut := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code != 1 || !strings.Contains(out+errOut, "synctang-keyholder init") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
}

func errorsIsNoKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), errNoKey.Error())
}

func hexOfScalarIn(t *testing.T, keyFile string) string {
	t.Helper()
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
