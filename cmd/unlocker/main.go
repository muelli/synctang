// SPDX-License-Identifier: AGPL-3.0-or-later

// Command unlocker is the machine side of synctang: it runs in the
// dracut initrd and, after boot, as a systemd password agent. It
// always listens for a key holder to dial in; it never dials out.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `usage: unlocker <command> [flags]

commands:
  enrol   add a key holder's public key to a LUKS volume
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "enrol":
		return runEnrol(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unlocker: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func runEnrol(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("enrol", flag.ContinueOnError)
	fs.SetOutput(stderr)
	device := fs.String("device", "", "LUKS2 device or image to enrol")
	pubkeyHex := fs.String("pubkey", "", "recipient's public key, hex-encoded (from keyholder export-pubkey)")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	name := fs.String("name", "", "this machine's name, shown to the key holder")
	transportID := fs.String("transport-id", "", "this machine's transport identity")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *device == "" || *pubkeyHex == "" || *passphraseFile == "" {
		fmt.Fprintln(stderr, "unlocker: --device, --pubkey and --existing-passphrase-file are all required")
		return 2
	}

	pubkey, err := hex.DecodeString(*pubkeyHex)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: --pubkey: %v\n", err)
		return 2
	}

	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}
	defer wipe(passphrase)

	if err := enrolRecipient(*device, passphrase, pubkey, *name, *transportID); err != nil {
		fmt.Fprintf(stderr, "unlocker: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "enrolled a new recipient on %s\n", *device)
	return 0
}

func readPassphrase(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
