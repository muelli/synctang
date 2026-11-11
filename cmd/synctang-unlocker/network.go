// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"github.com/muelli/synctang/internal/ui"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/muelli/synctang/mrcore/transport"
)

// defaultNetworkConfFile is where an owner who does not want the public
// Syncthing relay pool and discovery servers says which ones to use. The
// dracut module carries it into the initrd, because the agent there has
// no other place to read it from.
const defaultNetworkConfFile = "/etc/synctang/network.conf"

// procCmdline is the kernel command line. A variable so tests can point
// it elsewhere.
var procCmdline = "/proc/cmdline"

// The kernel command line arguments that override network.conf. They
// are the way to change the servers at the boot prompt, when the network
// path is what is broken, as rd.synctang=0 is the way to switch the
// agent off.
const (
	cmdlineRelay     = "rd.synctang.relay="
	cmdlineDiscovery = "rd.synctang.discovery="
)

// loadNetworkConfig reads the settings from confPath and from the
// kernel command line at cmdlinePath, the command line winning. Either
// path may be empty or name a file that is not there, which is not an
// error: most machines use the public servers and have neither. A file
// that is there and wrong is an error, never a quiet fall back to the
// defaults, which would announce a machine on the public servers
// whose owner had chosen otherwise.
func loadNetworkConfig(confPath, cmdlinePath string) (transport.NetworkConfig, error) {
	var file transport.NetworkConfig
	if confPath != "" {
		f, err := os.Open(confPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return transport.NetworkConfig{}, fmt.Errorf(T("reading %s: %w"), confPath, err)
		default:
			defer f.Close()
			file, err = transport.ParseNetworkConf(f)
			if err != nil {
				return transport.NetworkConfig{}, fmt.Errorf("%s: %w", confPath, err)
			}
		}
	}

	var cmd transport.NetworkConfig
	if cmdlinePath != "" {
		raw, err := os.ReadFile(cmdlinePath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return transport.NetworkConfig{}, fmt.Errorf(T("reading %s: %w"), cmdlinePath, err)
		default:
			cmd, err = parseCmdlineNetwork(string(raw))
			if err != nil {
				return transport.NetworkConfig{}, err
			}
		}
	}

	return file.Override(cmd), nil
}

// parseCmdlineNetwork picks rd.synctang.relay= and rd.synctang.discovery=
// out of a kernel command line. Arguments are split on white space, as
// the kernel does; neither value can contain any.
func parseCmdlineNetwork(cmdline string) (transport.NetworkConfig, error) {
	var relay, discovery string
	for _, arg := range strings.Fields(cmdline) {
		switch {
		case strings.HasPrefix(arg, cmdlineRelay):
			relay = strings.TrimPrefix(arg, cmdlineRelay)
		case strings.HasPrefix(arg, cmdlineDiscovery):
			discovery = strings.TrimPrefix(arg, cmdlineDiscovery)
		}
	}
	c, err := transport.ParseNetworkConfig(relay, discovery, "")
	if err != nil {
		// The setting's name in the error is the file's key; say
		// which kernel argument it came from instead.
		msg := err.Error()
		msg = strings.Replace(msg, transport.NetworkKeyRelayPool+":", strings.TrimSuffix(cmdlineRelay, "=")+":", 1)
		msg = strings.Replace(msg, transport.NetworkKeyDiscovery+":", strings.TrimSuffix(cmdlineDiscovery, "=")+":", 1)
		return transport.NetworkConfig{}, errors.New(msg)
	}
	return c, nil
}

// resolveNetwork is what agent, pair and open do at startup: load the
// settings, say which non-default servers are in use, and report a
// setting that is wrong rather than carry on with the defaults. The
// bool is false when the command should stop.
func resolveNetwork(confPath string, stdout, stderr io.Writer) (transport.NetworkConfig, bool) {
	c, err := loadNetworkConfig(confPath, procCmdline)
	if err != nil {
		ui.Errorf(stderr, T("network settings: %v\n"), err)
		return transport.NetworkConfig{}, false
	}
	if s := c.Summary(); s != "" {
		ui.Infof(stdout, "%s\n", s)
	}
	return c, true
}
