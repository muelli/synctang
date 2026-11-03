// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

func TestGenerateAndSaveWritesA32ByteScalar(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")

	s, err := generateAndSave(keyFile, false)
	if err != nil {
		t.Fatalf("generateAndSave: %v", err)
	}
	if len(s) != 32 {
		t.Fatalf("scalar length: got %d want 32", len(s))
	}

	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode: got %o want 600", perm)
	}
}

func TestGenerateAndSaveRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")

	if _, err := generateAndSave(keyFile, false); err != nil {
		t.Fatalf("first generateAndSave: %v", err)
	}

	if _, err := generateAndSave(keyFile, false); err == nil {
		t.Fatal("expected an error overwriting an existing key without force, got nil")
	}
}

func TestGenerateAndSaveForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")

	first, err := generateAndSave(keyFile, false)
	if err != nil {
		t.Fatalf("first generateAndSave: %v", err)
	}

	second, err := generateAndSave(keyFile, true)
	if err != nil {
		t.Fatalf("forced generateAndSave: %v", err)
	}

	if hex.EncodeToString(first) == hex.EncodeToString(second) {
		t.Fatal("forced regeneration produced the same scalar twice, randomness is broken")
	}
}

func TestLoadPrivateKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")

	want, err := generateAndSave(keyFile, false)
	if err != nil {
		t.Fatalf("generateAndSave: %v", err)
	}

	got, err := loadPrivateKey(keyFile)
	if err != nil {
		t.Fatalf("loadPrivateKey: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("loaded scalar does not match saved scalar:\n got=%x\n want=%x", got, want)
	}
}

func TestLoadPrivateKeyMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := loadPrivateKey(filepath.Join(dir, "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error loading a nonexistent key file, got nil")
	}
}

func TestLoadPrivateKeyRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte("not a hex scalar\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := loadPrivateKey(keyFile); err == nil {
		t.Fatal("expected an error loading a garbage key file, got nil")
	}
}

func TestPublicKeyHexMatchesGroup(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")

	s, err := generateAndSave(keyFile, false)
	if err != nil {
		t.Fatalf("generateAndSave: %v", err)
	}

	pubHex, err := publicKeyHex(keyFile)
	if err != nil {
		t.Fatalf("publicKeyHex: %v", err)
	}

	want, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	if pubHex != hex.EncodeToString(want) {
		t.Fatalf("publicKeyHex does not match S computed from the saved scalar:\n got=%s\n want=%x", pubHex, want)
	}
}
