// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/internal/ui"
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
	// Location says in one line where the key is, for the debug log: a path, or the name of an item.
	Location() string
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

// scalarFromHex decodes what the stores keep: the scalar as hex, with or without a newline. It
// takes bytes, not a string, so that the caller can wipe what it read; the caller still wipes text.
func scalarFromHex(text []byte, where string) ([]byte, error) {
	text = bytes.TrimSpace(text)
	s := make([]byte, hex.DecodedLen(len(text)))
	n, err := hex.Decode(s, text)
	if err != nil {
		mrcore.Wipe(s)
		return nil, fmt.Errorf("%s does not contain a valid hex scalar: %w", where, err)
	}
	if n != 32 {
		mrcore.Wipe(s)
		return nil, fmt.Errorf("%s contains a %d-byte scalar, want 32", where, n)
	}
	return s[:n], nil
}

// hexBytes is the scalar as hex text in a slice the caller wipes, which hex.EncodeToString would
// not allow.
func hexBytes(scalar []byte) []byte {
	out := make([]byte, hex.EncodedLen(len(scalar)))
	hex.Encode(out, scalar)
	return out
}

// fileStore keeps the scalar in a file, as hex, mode 0600.
type fileStore struct{ path string }

func newFileStore(path string) *fileStore { return &fileStore{path: path} }

func (f *fileStore) Name() string { return "file" }

func (f *fileStore) Location() string { return "file " + f.path }

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
	return scalarFromHex(data, "key file "+f.path)
}

func (f *fileStore) Save(scalar []byte, overwrite bool) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}
	text := append(hexBytes(scalar), '\n')
	defer mrcore.Wipe(text)
	if !overwrite {
		// O_EXCL: the check and the creation are one step.
		out, err := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists, use --force to overwrite", f.path)
		}
		if err != nil {
			return fmt.Errorf("writing key file: %w", err)
		}
		if _, err := out.Write(text); err != nil {
			out.Close()
			return fmt.Errorf("writing key file: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("writing key file: %w", err)
		}
		return nil
	}
	// Replacing goes through a new file, which is 0600 whatever the old one was, and a rename, so a
	// crash leaves the old key or the new one and never half of either.
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".key-")
	if err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}
	_, werr := tmp.Write(text)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("writing key file: %w", err)
	}
	if err := os.Rename(tmp.Name(), f.path); err != nil {
		os.Remove(tmp.Name())
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

// Every key has a record beside it, keyFile + ".store": the name of the store that holds it, and the public
// key it was made with ("public <hex>"). The record holds no secret. The store name says where to look. The
// public key is what a key that a store hands back is checked against: a key that is well formed and is not
// this key holder's own (another program wrote the item, a backup was restored over it) is refused, instead of
// being used until a machine says no. It guards against mix-ups, not against a program that can also edit
// the record, which can do anything that the user can.

func storeRecordPath(keyFile string) string { return keyFile + ".store" }

func writeStoreRecord(keyFile, spec, publicHex string) error {
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return err
	}
	text := spec + "\n"
	if publicHex != "" {
		text += "public " + publicHex + "\n"
	}
	return os.WriteFile(storeRecordPath(keyFile), []byte(text), 0o600)
}

// readStoreRecord returns the store recorded for keyFile; false when there is no record.
func readStoreRecord(keyFile string) (string, bool, error) {
	spec, _, recorded, err := readRecord(keyFile)
	return spec, recorded, err
}

// readRecord is the record: the store, the public key hex (empty when none was recorded), and whether
// there is a record at all.
func readRecord(keyFile string) (spec, publicHex string, recorded bool, err error) {
	raw, err := os.ReadFile(storeRecordPath(keyFile))
	if os.IsNotExist(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if i == 0 {
			spec = line
		} else if v, ok := strings.CutPrefix(line, "public "); ok {
			publicHex = strings.TrimSpace(v)
		}
	}
	return spec, publicHex, true, nil
}

// debugOut is where the debug log goes: standard error when --verbose (or SYNCTANG_DEBUG) asked for it,
// and nowhere otherwise.
var debugOut io.Writer = io.Discard

func debugf(format string, args ...any) { ui.Debugf(debugOut, format, args...) }

// errWrongKey is a key that a store holds and that is not the one this key holder was made with.
var errWrongKey = errors.New("is not the key this key holder was made with")

// verifyKey checks the scalar s that st returned against the public key recorded when the key was made. No
// recorded public key (a record without one) is nothing to check against, and passes.
func verifyKey(keyFile string, st keyStore, s []byte) error {
	_, recorded, _, err := readRecord(keyFile)
	if err != nil {
		return err
	}
	if recorded == "" {
		debugf("no public key is recorded in %s, so the key cannot be compared with one\n", storeRecordPath(keyFile))
		return nil
	}
	pub, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		return fmt.Errorf("computing the public key of the key %s holds: %w", st.Name(), err)
	}
	if got := hex.EncodeToString(pub); got != recorded {
		short := func(h string) string { return h[:min(len(h), 12)] + "..." }
		return fmt.Errorf("the key held by %s %w: its public key is %s, and %s says %s. Another program may have replaced it, "+
			"or the item belongs to another key file", st.Name(), errWrongKey, short(got), storeRecordPath(keyFile), short(recorded))
	}
	debugf("its public key matches the one recorded in %s\n", storeRecordPath(keyFile))
	return nil
}

// storeFromSpec makes the store a --store value or a record names, for the key at keyFile.
func storeFromSpec(spec, keyFile string) (keyStore, error) {
	switch spec {
	case "file":
		return newFileStore(keyFile), nil
	case "secret-service":
		return newSecretServiceStore(keyFile), nil
	case "systemd-creds":
		return newCredStore(credBlobPath(keyFile, false), false), nil
	case "systemd-creds:tpm2":
		return newCredStore(credBlobPath(keyFile, true), true), nil
	}
	return nil, fmt.Errorf("unknown store %q (named in %s)", spec, storeRecordPath(keyFile))
}

// credBlobPath is where a credential store's blob lives. The two variants have their own, so that
// replacing one with the other cannot delete the key it has just made.
func credBlobPath(keyFile string, tpm2 bool) string {
	if tpm2 {
		return keyFile + ".tpm2.cred"
	}
	return keyFile + ".cred"
}

// openStore is the store that holds the key for keyFile: the one its record names, or, with no
// record, the plain file keyFile if that is there.
func openStore(keyFile string) (keyStore, error) {
	spec, recorded, err := readStoreRecord(keyFile)
	if err != nil {
		return nil, err
	}
	if recorded {
		debugf("%s names the store %q for the key file %s\n", storeRecordPath(keyFile), spec, keyFile)
		return storeFromSpec(spec, keyFile)
	}
	debugf("there is no record %s, so the key is looked for in the plain file %s\n", storeRecordPath(keyFile), keyFile)
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
			return nil, "", fmt.Errorf("--store secret-service was asked for, but there is no keyring to ask: %s", keyringReason(err))
		}
		return newSecretServiceStore(keyFile), "", nil
	case "systemd-creds", "systemd-creds:tpm2":
		tpm := spec == "systemd-creds:tpm2"
		if err := credsAvailable(tpm); err != nil {
			return nil, "", fmt.Errorf("--store %s was asked for, but it cannot work here: %w", spec, err)
		}
		return newCredStore(credBlobPath(keyFile, tpm), tpm), "", nil
	case "auto":
		if err := keyringAvailable(); err != nil {
			return newFileStore(keyFile), fmt.Sprintf("no keyring is available in this session (%s), so the key is kept in a plain file", keyringReason(err)), nil
		}
		return newSecretServiceStore(keyFile), "", nil
	}
	return nil, "", fmt.Errorf("unknown store %q for --store: want one of %s", spec, storeNames)
}
