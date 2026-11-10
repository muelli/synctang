// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/mrcore"
	"github.com/zalando/go-keyring"
)

// secretServiceName is the service the key's item is filed under.
const secretServiceName = "synctang-keyholder"

// secretServiceStore keeps the scalar, as hex, in the desktop's keyring through the freedesktop
// Secret Service (D-Bus): GNOME Keyring, the one this is run against and supports. One item for each key
// file, named by its absolute path, so that two key holders on one account (a test, a second
// identity) do not meet.
type secretServiceStore struct{ account string }

func newSecretServiceStore(keyFile string) *secretServiceStore {
	abs, err := filepath.Abs(keyFile)
	if err != nil {
		abs = keyFile
	}
	return &secretServiceStore{account: abs}
}

func (s *secretServiceStore) Name() string { return "secret-service" }

func (s *secretServiceStore) Location() string {
	return "Secret Service item \"" + secretServiceName + "\" for " + s.account
}

func (s *secretServiceStore) Describe() string {
	return "the desktop's keyring (the Secret Service, over D-Bus: GNOME Keyring), " +
		"item " + secretServiceName + " for " + s.account + ". It is encrypted with a key that your login unlocks, " +
		"so a copy of ~/.config, a backup or an image of the disk is useless without your login password, " +
		"and so is another user of this laptop. It stays unlocked for the whole session, so software running as you " +
		"can ask the keyring for it, and it needs a session bus: it does not work over plain ssh to the laptop"
}

// unreachable is an error from the keyring that is not "there is no such item": no bus, no
// service, a locked collection nobody unlocked. Said so, because "no key" would send a person
// to make a new one while theirs is simply out of reach.
func unreachable(err error) error {
	return fmt.Errorf("the keyring could not be reached (is a Secret Service running in this session, with the login keyring unlocked? "+
		"it needs a D-Bus session, which plain ssh does not have): %w", err)
}

func (s *secretServiceStore) Exists() (bool, error) {
	_, err := keyring.Get(secretServiceName, s.account)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, keyring.ErrNotFound):
		return false, nil
	}
	return false, unreachable(err)
}

func (s *secretServiceStore) Load() ([]byte, error) {
	stored, err := keyring.Get(secretServiceName, s.account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("%w in the keyring for %s", errNoKey, s.account)
	}
	if err != nil {
		return nil, unreachable(err)
	}
	// The library returned a string, which cannot be wiped; the copy made here can.
	text := []byte(stored)
	defer mrcore.Wipe(text)
	return scalarFromHex(text, "the keyring item for "+s.account)
}

func (s *secretServiceStore) Save(scalar []byte, overwrite bool) error {
	if !overwrite {
		ok, err := s.Exists()
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("the keyring already holds a key for %s, use --force to overwrite", s.account)
		}
	}
	// The library takes a string, which Go cannot wipe: unlike the file store's, this copy of
	// the key stays in memory until the garbage collector reuses it.
	if err := keyring.Set(secretServiceName, s.account, hexOf(scalar)); err != nil {
		return unreachable(err)
	}
	return nil
}

func (s *secretServiceStore) Delete() error {
	if err := keyring.Delete(secretServiceName, s.account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return unreachable(err)
	}
	return nil
}

// keyringReason turns what the keyring library says into what is wrong and what to do about it,
// for the causes people meet: no D-Bus session (a plain ssh login has none), no Secret Service
// running, or a login keyring that nobody unlocked. Anything else is passed on as it came.
func keyringReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "dbus-launch") || strings.Contains(msg, "DBUS_SESSION_BUS_ADDRESS") ||
		strings.Contains(msg, "/bus: connect") || strings.Contains(msg, "session bus"):
		return "there is no D-Bus session here, which a desktop login has and a plain ssh login does not"
	case strings.Contains(msg, "org.freedesktop.secrets") && strings.Contains(msg, "provided"):
		return "no Secret Service is running: start GNOME Keyring"
	case strings.Contains(msg, "unlock"):
		return "the login keyring is locked and nobody unlocked it: " + msg
	}
	return msg
}

// secretServiceAvailable reports whether there is a keyring to ask, as nil or the reason. A
// probe for an item that does not exist: "not found" means the service answered.
func secretServiceAvailable() error {
	if _, err := keyring.Get(secretServiceName, "availability-probe"); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

func hexOf(scalar []byte) string {
	b := hexBytes(scalar)
	defer mrcore.Wipe(b)
	return string(b)
}
