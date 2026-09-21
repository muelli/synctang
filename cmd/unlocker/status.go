// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// runStatus answers the two questions an operator actually has about an
// enrolled volume: who can unlock it, and which identity does it answer
// to.
//
// Both were previously only available by reading the LUKS2 token JSON by
// hand, which matters most in the one situation where being wrong is
// expensive: revoking a key holder. "enrol --remove" takes a transport
// id and destroys the keyslot belonging to it, so naming the wrong one
// destroys the wrong key holder's access.
//
// Deliberately read-only, including of this machine's own identity: an
// operator asking what the state is must not change it. The agent
// creates the identity when it first needs one; status only reports
// whether that has happened.
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	device := fs.String("device", "", "LUKS2 device or image to inspect (auto-discovered from /proc/partitions if omitted)")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	activeDevice := *device
	if activeDevice == "" {
		found, err := findMr1Device()
		if err != nil {
			fmt.Fprintf(stderr, "unlocker: %v\n", err)
			return 1
		}
		activeDevice = found
	}

	fmt.Fprintf(stdout, "device: %s\n", activeDevice)

	// Read rather than load-or-create: see the comment above.
	switch cert, err := readCertIfPresent(*machineKeyFile); {
	case err != nil:
		fmt.Fprintf(stderr, "unlocker: reading %s: %v\n", *machineKeyFile, err)
		return 1
	case cert == nil:
		fmt.Fprintf(stdout, "this machine's transport id: not created yet (%s)\n", *machineKeyFile)
	default:
		fmt.Fprintf(stdout, "this machine's transport id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))
	}

	dump, err := runCommand(nil, "cryptsetup", "luksDump", activeDevice)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: reading the LUKS2 header: %v\n", err)
		return 1
	}

	tok, tokenID, err := loadToken(string(dump), activeDevice, "", "")
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	if tokenID < 0 || len(tok.Recipients) == 0 {
		fmt.Fprintln(stdout, "no key holder is enrolled on this device")
		return 0
	}

	if tok.Machine.Name != "" {
		fmt.Fprintf(stdout, "machine name shown to key holders: %s\n", tok.Machine.Name)
	}
	fmt.Fprintf(stdout, "mr-1 token: %d\n", tokenID)
	fmt.Fprintf(stdout, "enrolled key holders: %d\n", len(tok.Recipients))

	for i, rec := range tok.Recipients {
		id, ok := rec.TransportDeviceID()
		if !ok || id == "" {
			// Worth saying rather than skipping: a recipient with no
			// transport id can never be connected to, and cannot be
			// named to "enrol --remove" either.
			id = "(none recorded: cannot be dialled or revoked by id)"
		}
		keyslot := "(unknown)"
		if i < len(tok.Keyslots) {
			keyslot = tok.Keyslots[i]
		}
		fmt.Fprintf(stdout, "  %s  keyslot %s  kid %s\n", id, keyslot, shortKid(rec.Kid))
	}

	return 0
}

// readCertIfPresent loads a transport identity only if it already
// exists, returning (nil, nil) when it does not. transport.
// LoadOrCreateCert would create one, which is exactly what a status
// command must not do.
func readCertIfPresent(path string) (*tls.Certificate, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cert, err := transport.LoadOrCreateCert(path)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// shortKid renders a key holder's kid short enough to scan down a
// column, since its only use here is telling two recipients apart.
func shortKid(kid []byte) string {
	const shown = 8
	s := fmt.Sprintf("%x", kid)
	if len(s) <= shown {
		return s
	}
	return s[:shown] + strings.Repeat(".", 3)
}
