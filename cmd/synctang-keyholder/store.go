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
var errNoKey = errors.New("no key")

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
