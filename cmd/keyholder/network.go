// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/muelli/synctang/mrcore/transport"
)

// defaultConfigFile is where a key holder that does not want the public
// Syncthing discovery server says which one to use: the same keys as
// the machine's network.conf (relay-pool, discovery, discovery-announce),
// so one file can be copied to both.
func defaultConfigFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding config directory: %w", err)
	}
	return filepath.Join(dir, "keyholder", "config"), nil
}

// loadNetworkConfig reads the settings from the config file at path and
// then lets the flags override them. A file that is not there is the
// ordinary case and means the defaults, unless explicit says the person
// named it, when it is a mistake. A setting that is wrong is an error,
// never a quiet fall back to the public servers.
func loadNetworkConfig(path string, explicit bool, relayPool, discovery, announce string) (transport.NetworkConfig, error) {
	var file transport.NetworkConfig
	if path != "" {
		f, err := os.Open(path)
		switch {
		case errors.Is(err, fs.ErrNotExist) && !explicit:
		case err != nil:
			return transport.NetworkConfig{}, fmt.Errorf("reading %s: %w", path, err)
		default:
			defer f.Close()
			file, err = transport.ParseNetworkConf(f)
			if err != nil {
				return transport.NetworkConfig{}, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	flags, err := transport.ParseNetworkConfig(relayPool, discovery, announce)
	if err != nil {
		return transport.NetworkConfig{}, fmt.Errorf("--%w", err)
	}
	return file.Override(flags), nil
}
