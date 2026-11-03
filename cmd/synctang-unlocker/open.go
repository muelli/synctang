// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// "synctang-unlocker open" is for a volume that is not the machine's own root: an
// external drive plugged in after boot, say. The agent answers a password
// request that systemd made, and so only exists to be started when systemd is
// asking; here a person runs one command, a key holder approves, and the volume
// is opened directly with the recovered secret, then the command exits. Nothing
// stays running and nothing waits on the network unless somebody asked.

// Seams for the tests, which run without root and without device-mapper.
var (
	// openVolume opens device as /dev/mapper/name with key.
	openVolume = cryptsetupOpen
	// mapperExists reports whether /dev/mapper/name is already there.
	mapperExists = func(name string) bool {
		_, err := os.Stat("/dev/mapper/" + name)
		return err == nil
	}
	geteuid = os.Geteuid
)

// defaultOpenTimeout is how long the command waits for a key holder: long
// enough to find a phone and approve, short enough that a forgotten command does
// not sit listening for ever.
const defaultOpenTimeout = 5 * time.Minute

// mapperNamePattern is what a device-mapper name may be here. Narrower than
// what the kernel allows, so that a name can never be mistaken for an option or
// a path.
var mapperNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func validMapperName(name string) bool { return mapperNamePattern.MatchString(name) }

// cryptsetupOpen is the real openVolume. The key goes over a pipe, like every
// other secret handed to cryptsetup here, and never touches a file.
func cryptsetupOpen(device, name string, key []byte) error {
	_, err := runCommandWithSecrets([][]byte{key}, "cryptsetup",
		func(paths []string) []string {
			return []string{"open", "--type", "luks2", "--key-file=" + paths[0], device, name}
		})
	return err
}

// defaultMapperName names the opened volume after the volume, so two drives
// opened without a name do not collide and the same drive gets the same name
// each time.
func defaultMapperName(dump string) string {
	for _, line := range strings.Split(dump, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "UUID" {
			continue
		}
		uuid := strings.TrimSpace(value)
		if len(uuid) >= 8 {
			return "synctang-" + uuid[:8]
		}
	}
	return "synctang-volume"
}

const openUsage = `usage: synctang-unlocker open [flags] <device> [name]

Waits for a key holder to approve, then opens the LUKS volume on <device> as
/dev/mapper/<name> and exits. Mount it afterwards, and close it with
"cryptsetup close <name>". Needs root. The volume must already have a key holder
enrolled (see "synctang-unlocker pair").

`

func runOpen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machineName := fs.String("name", "", "this machine's name, shown to the key holder (default: the name given at enrolment)")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	timeout := fs.Duration("timeout", defaultOpenTimeout, "give up waiting for a key holder after this long")
	directAddr := fs.String("direct-addr", "", "listen on this address directly, bypassing relay and discovery (testing only)")
	networkConf := fs.String("network-conf", defaultNetworkConfFile, "file naming the relay pool and discovery servers to use instead of the public ones (the kernel arguments rd.synctang.relay= and rd.synctang.discovery= override it)")
	fs.Usage = func() { fmt.Fprint(stderr, openUsage); fs.PrintDefaults() }
	// Flags may come after the positional arguments, as people type them.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) < 1 || len(positional) > 2 {
		fmt.Fprint(stderr, openUsage)
		return 2
	}
	device := positional[0]
	name := ""
	if len(positional) == 2 {
		name = positional[1]
		if !validMapperName(name) {
			fmt.Fprintf(stderr, "synctang-unlocker: %q is not a usable name (letters, digits, '_', '.' and '-', starting with a letter or digit, at most 63 characters)\n", name)
			return 2
		}
	}

	// Before anything waits: approving on a phone and then finding out that
	// nothing could be opened is the worst way to learn this.
	if geteuid() != 0 {
		fmt.Fprintln(stderr, "synctang-unlocker: opening a volume needs root (try sudo)")
		return 1
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: reading the LUKS header of %s: %v\n", device, err)
		return 1
	}
	if _, id, err := loadToken(string(dump), device, "", ""); err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	} else if id < 0 {
		fmt.Fprintf(stderr, "synctang-unlocker: %s has no key holder enrolled, so nobody could approve opening it; enrol one first with: synctang-unlocker pair --device %s --existing-passphrase-file ...\n", device, device)
		return 1
	}
	if name == "" {
		name = defaultMapperName(string(dump))
	}
	if mapperExists(name) {
		fmt.Fprintf(stderr, "synctang-unlocker: /dev/mapper/%s already exists; close it first or choose another name\n", name)
		return 1
	}

	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "synctang-unlocker: %v\n", err)
		return 1
	}
	network, ok := resolveNetwork(*networkConf, stdout, stderr)
	if !ok {
		return 1
	}
	return openOnDemand(device, name, *machineName, cert, *timeout, *directAddr, network, stdout, stderr)
}

// openOnDemand waits up to timeout for a key holder whose answer opens device.
// An attempt that fails (a key holder that declines, one that answers with the
// wrong key, a dropped connection) is reported and followed by another, as
// "synctang-unlocker pair" does, until one works or the time is up.
func openOnDemand(device, name, machineName string, cert tls.Certificate, timeout time.Duration, directAddr string, network transport.NetworkConfig, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "waiting for a key holder to approve opening %s as /dev/mapper/%s\n", device, name)
	fmt.Fprintf(stdout, "this machine's transport id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tr := newAgentTransport(cert, nil, directAddr, network)
	deliver := func(key []byte) error {
		if err := openVolume(device, name, key); err != nil {
			return fmt.Errorf("synctang-unlocker: opening %s: %w", device, err)
		}
		return nil
	}
	for {
		err := serveOneExchange(ctx, device, machineName, transport.ListenOptions{DirectAddr: directAddr}, tr, deliver)
		if err == nil {
			fmt.Fprintf(stdout, "unlocked: /dev/mapper/%s\n", name)
			return 0
		}
		if ctx.Err() != nil {
			fmt.Fprintf(stderr, "synctang-unlocker: nobody approved within %s\n", timeout)
			return 1
		}
		fmt.Fprintf(stderr, "synctang-unlocker: %v (still waiting)\n", err)
		select {
		case <-ctx.Done():
		case <-time.After(agentRetryInterval):
		}
	}
}
