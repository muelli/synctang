// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muelli/synctang/mrcore"
)

// defaultKeyFile is where the file backend keeps the long-term private
// scalar s, unless overridden. It lives under the user's config
// directory rather than the working directory, so it survives being
// run from anywhere and is not accidentally picked up by, say, a
// backup of a project checkout.
func defaultKeyFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding config directory: %w", err)
	}
	return filepath.Join(dir, "keyholder", "key"), nil
}

// defaultTransportKeyFile is where this key holder's persistent transport
// identity (its TLS certificate and key, see transport.LoadOrCreateCert)
// lives unless overridden. It is a separate file from defaultKeyFile: one
// is the long-term MR-1 scalar s, the other the Syncthing Device ID a
// machine's AuthorizedPeers and Recipient.Transport are matched against;
// they are both "this key holder's identity" but serve different layers.
func defaultTransportKeyFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding config directory: %w", err)
	}
	return filepath.Join(dir, "keyholder", "transport.pem"), nil
}

// generateAndSave creates a new random long-term scalar s and writes it
// to keyFile as hex, refusing to overwrite an existing key unless force
// is set: losing s means losing access to every volume enrolled against
// it, and a silent overwrite is exactly the kind of mistake that would
// not be noticed until the next reboot needed the old key.
func generateAndSave(keyFile string, force bool) ([]byte, error) {
	if !force {
		if _, err := os.Stat(keyFile); err == nil {
			return nil, fmt.Errorf("%s already exists, use --force to overwrite", keyFile)
		}
	}

	s, err := mrcore.P256().RandomScalar()
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, fmt.Errorf("creating key directory: %w", err)
	}

	encoded := hex.EncodeToString(s) + "\n"
	if err := os.WriteFile(keyFile, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("writing key file: %w", err)
	}

	return s, nil
}

// loadPrivateKey reads the long-term scalar s back from keyFile.
func loadPrivateKey(keyFile string) ([]byte, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	defer wipe(data)

	s, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("key file %s does not contain a valid hex scalar: %w", keyFile, err)
	}
	if len(s) != 32 {
		return nil, fmt.Errorf("key file %s contains a %d-byte scalar, want 32", keyFile, len(s))
	}

	return s, nil
}

// publicKeyHex loads s from keyFile and returns S = s.G, hex-encoded,
// suitable for pasting into `unlocker enrol --pubkey`.
func publicKeyHex(keyFile string) (string, error) {
	s, err := loadPrivateKey(keyFile)
	if err != nil {
		return "", err
	}
	defer wipe(s)

	S, err := mrcore.P256().ScalarBaseMult(s)
	if err != nil {
		return "", fmt.Errorf("computing public key: %w", err)
	}

	return hex.EncodeToString(S), nil
}
