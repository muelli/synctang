// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/ui"
	"io"
	"os"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/wrap"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// runStatus says where the key is kept and what that protects, in plain words, and whether it
// can be read. Read only: it makes no key, no record and no identity, and prints no secret.
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	keyFile := keyFileFlag(fs)
	transportKeyFile := transportKeyFileFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" || *transportKeyFile == "" {
		ui.Errorln(stderr, "--key-file and --transport-key-file are required (could not determine a default)")
		return 2
	}

	st, err := openStore(*keyFile)
	if err != nil {
		if errors.Is(err, errNoKey) {
			ui.Errorf(stderr, "%v; make one with: synctang-keyholder init\n", err)
		} else {
			ui.Errorf(stderr, "%v\n", err)
		}
		return 1
	}
	look := ui.For(stdout)
	fmt.Fprintf(stdout, "%skey: held by %s\n%s\n", look.Emoji("🔑", ""), look.Bold(st.Name()), look.Dim(wrap.Paragraph(st.Describe(), "  ", wrap.Width(stdout))))

	// Loaded to say whether it can be, and wiped at once. For a keyring this may ask for the
	// login password, which is the information: it is what an unlock will ask too.
	if s, err := st.Load(); err != nil {
		fmt.Fprintln(stdout, look.Line(look.Fail(""), look.Red(fmt.Sprintf("readable: no (%v)", err))))
	} else {
		verr := verifyKey(*keyFile, st, s)
		mrcore.Wipe(s)
		if verr != nil {
			fmt.Fprintln(stdout, look.Line(look.Fail(""), look.Red(fmt.Sprintf("readable: no (%v)", verr))))
		} else {
			fmt.Fprintln(stdout, look.Line(look.OK(""), "readable: yes"))
			if _, recorded, _, _ := readRecord(*keyFile); recorded != "" {
				fmt.Fprintln(stdout, look.Dim("  it matches the public key recorded when it was made"))
			} else {
				fmt.Fprintln(stdout, look.Dim("  no public key was recorded, so a replaced key would not be noticed"))
			}
		}
	}

	if _, err := os.Stat(*transportKeyFile); err != nil {
		fmt.Fprintf(stdout, "transport identity: not created yet (%s)\n", *transportKeyFile)
		return 0
	}
	cert, err := transport.LoadOrCreateCert(*transportKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "transport identity: %s\n", look.Dim(protocol.NewDeviceID(cert.Certificate[0]).String()))
	return 0
}
