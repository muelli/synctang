// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/internal/ui"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// extractUsage is what "extract-keyslot-key -h" prints. The command is not in the usage text of the program
// or in its shell completion, and the manual page describes it: it shows what an approval can do, for
// whoever wants to judge that, and is not a step in setting anything up.
var extractUsage = T(`usage: synctang-unlocker extract-keyslot-key [flags] <device>
       synctang-unlocker extract-keyslot-key --device <device> [flags]

Waits for a key holder to approve, as "open" does, and prints the passphrase of that key holder's own
keyslot on standard output, and nothing else there. It does not open the volume. Anyone who has this
passphrase can unlock the volume without any key holder, add or destroy keyslots and read out the
volume key, so handle it as the LUKS passphrase itself: do not put it in a file. Needs root. The manual
page says what ends it.
`)

// runExtractKeyslotKey is "synctang-unlocker extract-keyslot-key": the secret a key holder's approval
// recovers is the passphrase of its keyslot (the agent hands exactly this to systemd), and the machine
// holds it for the moment of the unlock. This prints it, which is what "clevis luks pass" does for Tang.
func runExtractKeyslotKey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("extract-keyslot-key", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	deviceFlag := fs.String("device", "", T("the LUKS device whose keyslot key to extract, instead of giving it as the first argument"))
	machineName := fs.String("name", "", T("this machine's name, shown to the key holder (default: the name given at enrolment)"))
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, T("path to this machine's persistent transport identity"))
	timeout := fs.Duration("timeout", defaultOpenTimeout, T("give up waiting for a key holder after this long"))
	directAddr := fs.String("direct-addr", "", T("listen on this address directly, bypassing relay and discovery (testing only)"))
	networkConf := fs.String("network-conf", defaultNetworkConfFile, T("file naming the relay pool and discovery servers to use instead of the public ones (the kernel arguments rd.synctang.relay= and rd.synctang.discovery= override it)"))
	fs.Usage = func() { fmt.Fprint(stderr, extractUsage); fs.PrintDefaults() }
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
	if *deviceFlag != "" {
		positional = append([]string{*deviceFlag}, positional...)
	}
	if len(positional) != 1 {
		fmt.Fprint(stderr, extractUsage)
		return 2
	}

	key, code := keyFromApprovalFn(positional[0], approval{
		machineName: *machineName, machineKeyFile: *machineKeyFile, networkConf: *networkConf,
		directAddr: *directAddr, timeout: *timeout, what: T("extracting its key"), purpose: mrcore.PurposeExtract,
	}, stderr, stderr)
	if code != 0 {
		return code
	}
	defer mrcore.Wipe(key)
	// At a terminal the prompt would run into the key; to a pipe or $(...) nothing but the key is wanted.
	if _, err := stdout.Write(key); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	if ui.For(stdout) != (ui.Style{}) {
		fmt.Fprintln(stdout)
	}
	ui.Notef(stderr, T("that is the passphrase of the keyslot that approved: anyone who has it can unlock %s without a key holder, until that keyslot is destroyed\n"), positional[0])
	return 0
}

// keyFromApprovalFn is keyFromApproval; a variable so that a test can stand in for the exchange, which the tests
// of the commands that run it cover.
var keyFromApprovalFn = keyFromApproval

// approval is what waiting for an enrolled key holder's approval needs, the same as "open".
type approval struct {
	machineName, machineKeyFile, networkConf, directAddr string
	timeout                                              time.Duration
	// what the approval is for, for the sentence that says nobody could give it ("opening it").
	what string
	// purpose is what the machine tells the key holder it will do with the answer (mrcore.Purpose...).
	purpose string
}

// keyFromApproval waits for a key holder whose answer opens device and returns what it recovered, the
// passphrase of that key holder's keyslot (the caller wipes it), or a status for the program to exit with.
// What it says goes to info, so that a command that prints the key on standard output has it clean.
func keyFromApproval(device string, a approval, info, stderr io.Writer) ([]byte, int) {
	// Before anything waits: approving on a phone and then finding out that nothing could be done is the
	// worst way to learn this.
	if geteuid() != 0 {
		ui.Errorf(stderr, T("%s needs root; %s\n"), a.what, rootHint())
		return nil, 1
	}
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		ui.Errorf(stderr, T("reading the LUKS header of %s: %v%s\n"), device, err, permissionHint(err))
		return nil, 1
	}
	if why, err := whyNobodyCouldApprove(string(dump), device, a.what); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return nil, 1
	} else if why != "" {
		ui.Errorf(stderr, "%s", why)
		return nil, 1
	}
	cert, err := transport.LoadOrCreateCert(a.machineKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return nil, 1
	}
	network, ok := resolveNetwork(a.networkConf, info, stderr)
	if !ok {
		return nil, 1
	}
	fmt.Fprintf(info, T("waiting for a key holder to approve %s\n"), a.what)
	fmt.Fprintf(info, T("this machine's transport id: %s\n"), protocol.NewDeviceID(cert.Certificate[0]))

	var key []byte
	deliver := func(keyMaterial []byte) error {
		key = append([]byte(nil), keyMaterial...) // the caller wipes the slice it was lent
		return nil
	}
	if !waitForApproval(device, a.machineName, a.purpose, cert, a.timeout, a.directAddr, network, deliver, stderr) {
		return nil, 1
	}
	return key, 0
}
