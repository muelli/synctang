// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/mrcore"
)

// errNoKey says there is no key in the store yet, as opposed to a key that could not be read.
var errNoKey = errors.New("there is no key")

// keyStore is where the long-term scalar s lives. Which store holds it decides what a copy of the
// key holder's files is worth to somebody who is not its owner, so each says, in its Describe,
// what it protects against and what it does not, and none is ever called secure.
type keyStore interface {
	// Name is the store's name as --store spells it.
	Name() string
	// Describe says in plain words what the store protects the key against and what it does not.
	Describe() string
	// Exists reports whether the store holds a key, without reading it where it can be avoided.
	Exists() (bool, error)
	// Load returns the 32-byte scalar, which the caller wipes. A missing key is errNoKey.
	Load() ([]byte, error)
	// Save stores the scalar. It refuses to replace an existing key unless overwrite is set:
	// losing s means losing access to every volume enrolled against it, and a silent overwrite
	// would not be noticed until the next reboot needed the old key.
	Save(scalar []byte, overwrite bool) error
	// Delete removes the key; deleting what is not there is not an error.
	Delete() error
}

// scalarFromHex decodes what the stores keep: the scalar as hex, with or without a newline.
func scalarFromHex(text, where string) ([]byte, error) {
	s, err := hex.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("%s does not contain a valid hex scalar: %w", where, err)
	}
	if len(s) != 32 {
		mrcore.Wipe(s)
		return nil, fmt.Errorf("%s contains a %d-byte scalar, want 32", where, len(s))
	}
	return s, nil
}

// fileStore keeps the scalar in a file, as hex, mode 0600.
type fileStore struct{ path string }

func newFileStore(path string) *fileStore { return &fileStore{path: path} }

func (f *fileStore) Name() string { return "file" }

func (f *fileStore) Describe() string {
	return "a plain file (" + f.path + ", mode 0600). It keeps out other users of this laptop and nothing else: " +
		"a copy of the file, from a backup or a synced folder or an image of the disk, is the key, " +
		"and software running as you can read it"
}

func (f *fileStore) Exists() (bool, error) {
	_, err := os.Stat(f.path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	}
	return false, err
}

func (f *fileStore) Load() ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("reading key file %s: %w", f.path, errNoKey)
	}
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	defer mrcore.Wipe(data)
	return scalarFromHex(string(data), "key file "+f.path)
}

func (f *fileStore) Save(scalar []byte, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(f.path); err == nil {
			return fmt.Errorf("%s already exists, use --force to overwrite", f.path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}
	if err := os.WriteFile(f.path, []byte(hex.EncodeToString(scalar)+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}
	return nil
}

func (f *fileStore) Delete() error {
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// A key that is not in a plain file at keyFile has a record beside it, keyFile + ".store",
// naming the store that holds it. The record holds no secret: it only says where to look.

func storeRecordPath(keyFile string) string { return keyFile + ".store" }

func writeStoreRecord(keyFile, spec string) error {
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(storeRecordPath(keyFile), []byte(spec+"\n"), 0o600)
}

// readStoreRecord returns the store recorded for keyFile; false when there is no record.
func readStoreRecord(keyFile string) (string, bool, error) {
	raw, err := os.ReadFile(storeRecordPath(keyFile))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(raw)), true, nil
}

// storeFromSpec makes the store a --store value or a record names, for the key at keyFile.
func storeFromSpec(spec, keyFile string) (keyStore, error) {
	switch spec {
	case "file":
		return newFileStore(keyFile), nil
	case "secret-service":
		return newSecretServiceStore(keyFile), nil
	case "systemd-creds":
		return newCredStore(keyFile+".cred", false), nil
	case "systemd-creds:tpm2":
		return newCredStore(keyFile+".cred", true), nil
	}
	return nil, fmt.Errorf("unknown store %q (named in %s)", spec, storeRecordPath(keyFile))
}

// openStore is the store that holds the key for keyFile: the one its record names, or, with no
// record, the plain file keyFile if that is there.
func openStore(keyFile string) (keyStore, error) {
	spec, recorded, err := readStoreRecord(keyFile)
	if err != nil {
		return nil, err
	}
	if recorded {
		return storeFromSpec(spec, keyFile)
	}
	f := newFileStore(keyFile)
	if ok, err := f.Exists(); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w at %s", errNoKey, keyFile)
	}
	return f, nil
}

// keyringAvailable is the probe init uses to choose a store; a variable so that tests can say
// whether there is a keyring.
var keyringAvailable = secretServiceAvailable

// storeNames are what --store accepts, in the order they are offered.
const storeNames = "auto, file, secret-service, systemd-creds, systemd-creds:tpm2"

// chooseStore is the store init puts a new key in. "auto" is the keyring when there is one and a
// plain file when there is not, and says so: the person is told what was chosen and why, and a
// store that was asked for by name is never replaced by another. The note is what to tell them.
func chooseStore(spec, keyFile string) (keyStore, string, error) {
	switch spec {
	case "file":
		return newFileStore(keyFile), "", nil
	case "secret-service":
		if err := keyringAvailable(); err != nil {
			return nil, "", fmt.Errorf("--store secret-service was asked for, but there is no keyring to ask: %w", err)
		}
		return newSecretServiceStore(keyFile), "", nil
	case "systemd-creds", "systemd-creds:tpm2":
		tpm := spec == "systemd-creds:tpm2"
		if err := credsAvailable(tpm); err != nil {
			return nil, "", fmt.Errorf("--store %s was asked for, but it cannot work here: %w", spec, err)
		}
		return newCredStore(keyFile+".cred", tpm), "", nil
	case "auto":
		if err := keyringAvailable(); err != nil {
			return newFileStore(keyFile), fmt.Sprintf("no keyring is available in this session (%v), so the key is kept in a plain file", err), nil
		}
		return newSecretServiceStore(keyFile), "", nil
	}
	return nil, "", fmt.Errorf("unknown store %q for --store: want one of %s", spec, storeNames)
}
