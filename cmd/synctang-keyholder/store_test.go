// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scalar32(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestFileStoreSavesAndLoadsTheScalar(t *testing.T) {
	st := newFileStore(filepath.Join(t.TempDir(), "sub", "key"))
	if err := st.Save(scalar32(7), false); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, scalar32(7)) {
		t.Fatalf("loaded %x", got)
	}
	if ok, err := st.Exists(); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
}

func TestFileStoreKeepsTheKeyOutOfOtherUsersReach(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "synctang-keyholder")
	st := newFileStore(filepath.Join(dir, "key"))
	if err := st.Save(scalar32(1), false); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "key"))
	di, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("key %v, directory %v; want 0600 and 0700", fi.Mode().Perm(), di.Mode().Perm())
	}
}

func TestFileStoreRefusesToOverwriteUnlessAsked(t *testing.T) {
	st := newFileStore(filepath.Join(t.TempDir(), "key"))
	if err := st.Save(scalar32(1), false); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(scalar32(2), false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal that names --force", err)
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

func TestFileStoreSaysThereIsNoKeyWhenThereIsNone(t *testing.T) {
	st := newFileStore(filepath.Join(t.TempDir(), "key"))
	if _, err := st.Load(); !errors.Is(err, errNoKey) {
		t.Fatalf("err = %v, want errNoKey", err)
	}
	if ok, err := st.Exists(); err != nil || ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
}

func TestFileStoreRefusesWhatIsNotAScalar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	for name, content := range map[string]string{"not hex": "zz\n", "wrong length": "abcd\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := newFileStore(path).Load(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFileStoreDeletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	st := newFileStore(path)
	_ = st.Save(scalar32(1), false)
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the key file is still there: %v", err)
	}
	if err := st.Delete(); err != nil {
		t.Fatalf("deleting what is not there is not an error: %v", err)
	}
}

func TestFileStoreDescribesItselfWithoutOverclaiming(t *testing.T) {
	st := newFileStore("/x/key")
	if st.Name() != "file" {
		t.Errorf("name %q", st.Name())
	}
	d := st.Describe()
	for _, want := range []string{"plain file", "other users", "software running as you"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description lacks %q: %s", want, d)
		}
	}
	if strings.Contains(strings.ToLower(d), "secure") {
		t.Errorf("a store is never called secure: %s", d)
	}
}

func TestFileStoreForceMakesTheFileOwnerOnlyEvenIfItWasNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newFileStore(path)
	if err := st.Save(scalar32(5), true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v: a replaced key file keeps the readability the old file had", info.Mode().Perm())
	}
	got, err := st.Load()
	if err != nil || !bytes.Equal(got, scalar32(5)) {
		t.Fatalf("%x %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestFileStoreWithoutForceNeverReplacesAndLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	st := newFileStore(path)
	if err := st.Save(scalar32(5), false); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(scalar32(6), false); err == nil {
		t.Fatal("replaced without --force")
	}
	if got, _ := st.Load(); !bytes.Equal(got, scalar32(5)) {
		t.Error("the refused save changed the file")
	}
}
