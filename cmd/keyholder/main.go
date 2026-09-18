// SPDX-License-Identifier: AGPL-3.0-or-later

// Command keyholder is the laptop key holder for synctang: it holds a
// long-term private scalar and answers a machine's recovery challenges
// on demand. There is no listening mode; the key holder always dials.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

const usage = `usage: keyholder <command> [flags]

commands:
  init            generate a new long-term key and transport identity
  export-pubkey   print the public key and transport id, for "unlocker enrol"
  unlock          answer one machine's recovery attempt
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "export-pubkey":
		return runExportPubkey(args[1:], stdout, stderr)
	case "unlock":
		return runUnlock(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "keyholder: unknown command %q\n%s", args[0], usage)
		return 2
	}
}

func keyFileFlag(fs *flag.FlagSet) *string {
	def, err := defaultKeyFile()
	if err != nil {
		// A missing config directory is unusual enough that failing the
		// flag default is fine; the user can still pass --key-file
		// explicitly.
		def = ""
	}
	return fs.String("key-file", def, "path to the long-term private key")
}

func transportKeyFileFlag(fs *flag.FlagSet) *string {
	def, err := defaultTransportKeyFile()
	if err != nil {
		def = ""
	}
	return fs.String("transport-key-file", def, "path to this key holder's persistent transport identity")
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	force := fs.Bool("force", false, "overwrite an existing key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --transport-key-file is required (could not determine a default)")
		return 2
	}

	s, err := generateAndSave(*keyFile, *force)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}
	defer wipe(s)

	// The transport identity is created alongside the MR-1 key: both
	// together are "this key holder's identity". Unlike the scalar, it is
	// always loaded rather than regenerated when --force is given: a fresh
	// certificate here would change this key holder's Device ID, silently
	// breaking every AuthorizedPeers list it was already enrolled into.
	if _, err := transport.LoadOrCreateCert(*transportKeyFile); err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "generated a new key at %s\n", *keyFile)
	fmt.Fprintf(stdout, "transport identity at %s\n", *transportKeyFile)
	return 0
}

func runExportPubkey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export-pubkey", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --transport-key-file is required (could not determine a default)")
		return 2
	}

	pubHex, err := publicKeyHex(*keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "S: %s\n", pubHex)
	fmt.Fprintf(stdout, "transport-id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))
	return 0
}

func runUnlock(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	directAddr := fs.String("direct-addr", "", "dial this address directly, bypassing relay and discovery (testing only)")
	yes := fs.Bool("yes", false, "skip the approval prompt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "keyholder: unlock requires exactly one argument, the machine's transport id")
		return 2
	}
	machineID := fs.Arg(0)

	if *keyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --key-file is required (could not determine a default)")
		return 2
	}
	if *transportKeyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --transport-key-file is required (could not determine a default)")
		return 2
	}

	s, err := loadPrivateKey(*keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		wipe(s)
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	if err := runUnlockOnce(context.Background(), s, cert, machineID, *directAddr, *yes, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}
	return 0
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
