// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/muelli/synctang/mrcore"
)

// configDirName is this program's directory under the user's config directory,
// holding key, transport.pem and config.
const configDirName = "synctang-keyholder"

// configDir is where the key holder keeps its files by default.
func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf(T("finding config directory: %w"), err)
	}
	return filepath.Join(dir, configDirName), nil
}

// defaultKeyFile is where the file backend keeps the long-term private
// scalar s, unless overridden. It lives under the user's config
// directory rather than the working directory, so it survives being
// run from anywhere and is not accidentally picked up by, say, a
// backup of a project checkout.
func defaultKeyFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "key"), nil
}

// defaultTransportKeyFile is where this key holder's persistent transport
// identity (its TLS certificate and key, see transport.LoadOrCreateCert)
// lives unless overridden. It is a separate file from defaultKeyFile: one
// is the long-term MR-1 scalar s, the other the Syncthing Device ID a
// machine's AuthorizedPeers and Recipient.Transport are matched against;
// they are both "this key holder's identity" but serve different layers.
func defaultTransportKeyFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "transport.pem"), nil
}

// generateAndSave creates a new random long-term scalar s and saves it in the store for keyFile,
// refusing to overwrite an existing key unless force is set (see keyStore.Save).
func generateAndSave(keyFile string, force bool) ([]byte, error) {
	s, err := mrcore.P256().RandomScalar()
	if err != nil {
		return nil, fmt.Errorf(T("generating key: %w"), err)
	}
	if err := newFileStore(keyFile).Save(s, force); err != nil {
		mrcore.Wipe(s)
		return nil, err
	}
	return s, nil
}

// loadPrivateKey reads the long-term scalar s back from the store for keyFile.
func loadPrivateKey(keyFile string) ([]byte, error) {
	st, err := openStore(keyFile)
	if err != nil {
		return nil, err
	}
	s, err := st.Load()
	if err != nil {
		debugf(T("could not read the key from %s (%s): %v\n"), st.Name(), st.Location(), err)
		return nil, err
	}
	debugf(T("read the key from %s (%s)\n"), st.Name(), st.Location())
	if err := verifyKey(keyFile, st, s); err != nil {
		mrcore.Wipe(s)
		return nil, err
	}
	return s, nil
}

// publicKeyHex loads s from keyFile and returns S = s.G, hex-encoded,
// suitable for pasting into `synctang-unlocker enrol --pubkey`.
func publicKeyHex(keyFile string) (string, error) {
	s, err := loadPrivateKey(keyFile)
	if err != nil {
		return "", err
	}
	defer mrcore.Wipe(s)

	S, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		return "", fmt.Errorf(T("computing public key: %w"), err)
	}

	return hex.EncodeToString(S), nil
}
