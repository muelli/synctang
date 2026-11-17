// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/ui"
)

// runNetworkArgs prints the kernel arguments that give the initrd the network this machine has now,
// for a machine whose address is set by hand (see bootnet.go). Only the arguments go to standard
// output, so that $(synctang-unlocker network-args) can be pasted as it is; on a DHCP network there
// are none to print, and the reason goes to standard error.
func runNetworkArgs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("network-args", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	iface := fs.String("interface", "", T("the interface to give the initrd (default: the one with the default route)"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		ui.Errorf(stderr, T("network-args takes no arguments, got %q\n"), fs.Arg(0))
		return 2
	}
	n, err := detectBootNetwork(*iface)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	if n.DHCP {
		ui.Noteln(stderr, fmt.Sprintf(T("%s gets its address by DHCP, which is what the initrd does; there is nothing to add to the kernel command line"), n.Dev))
		return 0
	}
	fmt.Fprintln(stdout, n.Args)
	return 0
}
