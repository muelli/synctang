// SPDX-License-Identifier: AGPL-3.0-or-later

// Command keyholder is the laptop key holder for synctang: it holds a
// long-term private scalar and answers a machine's recovery challenges
// on demand. There is no listening mode; the key holder always dials.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `usage: keyholder <command> [flags]

commands:
  init            generate a new long-term key
  export-pubkey   print the public key, for "unlocker enrol --pubkey"
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
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "export-pubkey":
		return runExportPubkey(args[1:], stdout, stderr)
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

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := keyFileFlag(fs)
	force := fs.Bool("force", false, "overwrite an existing key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --key-file is required (could not determine a default)")
		return 2
	}

	s, err := generateAndSave(*keyFile, *force)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}
	defer wipe(s)

	fmt.Fprintf(stdout, "generated a new key at %s\n", *keyFile)
	return 0
}

func runExportPubkey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export-pubkey", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := keyFileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" {
		fmt.Fprintln(stderr, "keyholder: --key-file is required (could not determine a default)")
		return 2
	}

	pubHex, err := publicKeyHex(*keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "keyholder: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, pubHex)
	return 0
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
