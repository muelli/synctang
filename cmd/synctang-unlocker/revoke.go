// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/ui"
	"github.com/muelli/synctang/mrcore"
)

// parseInterleaved parses a command line in which the options and the arguments may come in any order, as
// they may for other GNU programs: the flag package stops at the first argument, so what follows it is parsed
// again. It returns the arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// runRevoke is "synctang-unlocker revoke": it removes a key holder, destroying its keyslot and its entry in the
// token, and leaves the others alone.
func runRevoke(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	device := fs.String("device", "", "LUKS2 device or image to revoke the key holder on (\"synctang-unlocker devices\" lists them)")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	keyslot := fs.String("keyslot", "", "the keyslot of the entry to revoke: needed when several entries share the transport id, and the way to revoke one that has none (\"status\" says which)")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		ui.Errorf(stderr, "revoke takes one key holder, by its transport id (or --keyslot), not %d\n", len(positional))
		return 2
	}
	if *device == "" {
		ui.Errorf(stderr, "--device is required: %s\n", deviceHint())
		return 2
	}
	if *passphraseFile == "" {
		ui.Errorln(stderr, "--existing-passphrase-file is required")
		return 2
	}
	if len(positional) == 0 && *keyslot == "" {
		ui.Errorln(stderr, "revoke wants the key holder's transport id or --keyslot (\"synctang-unlocker status\" lists them)")
		return 2
	}
	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	defer mrcore.Wipe(passphrase)

	look := ui.For(stdout)
	if len(positional) == 0 {
		if err := removeRecipientByKeyslot(*device, passphrase, *keyslot); err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, look.Line(look.OK(""), fmt.Sprintf("revoked the key holder in keyslot %s on %s", *keyslot, *device)))
		return 0
	}
	if err := removeRecipient(*device, passphrase, positional[0], *keyslot); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, look.Line(look.OK(""), fmt.Sprintf("revoked recipient %s on %s", positional[0], *device)))
	return 0
}

// runRotate is "synctang-unlocker rotate": it gives an enrolled key holder a new key. The new key is enrolled
// before the old one is revoked, so the volume can be opened throughout, and the key holder keeps its transport
// identity: a rotation that lost it would leave an entry the agent cannot restrict to any peer.
func runRotate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	device := fs.String("device", "", "LUKS2 device or image the key holder is enrolled on (\"synctang-unlocker devices\" lists them)")
	pubkeyHex := fs.String("pubkey", "", "the key holder's new public key, hex-encoded (from synctang-keyholder export-pubkey)")
	passphraseFile := fs.String("existing-passphrase-file", "", "file containing an existing valid LUKS passphrase (- for stdin)")
	keyslot := fs.String("keyslot", "", "the keyslot of the entry to rotate: needed when several entries share the transport id")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	if *device == "" {
		ui.Errorf(stderr, "--device is required: %s\n", deviceHint())
		return 2
	}
	if *passphraseFile == "" {
		ui.Errorln(stderr, "--existing-passphrase-file is required")
		return 2
	}
	if *pubkeyHex == "" {
		ui.Errorln(stderr, "--pubkey is required: the key holder's new public key")
		return 2
	}
	if len(positional) != 1 {
		ui.Errorln(stderr, "rotate wants one key holder, by its transport id (\"synctang-unlocker status\" lists them)")
		return 2
	}
	pubkey, err := hex.DecodeString(*pubkeyHex)
	if err != nil {
		ui.Errorf(stderr, "--pubkey: %v\n", err)
		return 2
	}
	passphrase, err := readPassphrase(*passphraseFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	defer mrcore.Wipe(passphrase)

	if err := replaceRecipient(*device, passphrase, pubkey, positional[0], positional[0], *keyslot); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	look := ui.For(stdout)
	fmt.Fprintln(stdout, look.Line(look.OK(""), fmt.Sprintf("rotated recipient %s on %s; its previous key no longer opens the device", positional[0], *device)))
	return 0
}
