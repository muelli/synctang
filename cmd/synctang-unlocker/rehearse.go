// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/ui"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// "synctang-unlocker rehearse" is what to run before the first reboot of a machine that was
// just set up, for a person who is, reasonably, nervous about it: the unlock as it will happen
// at boot, on the running system. It checks what the initrd needs, then waits for a key holder
// to approve and checks that what it recovers opens the volume, which is the agent's own
// exchange, and it maps nothing: the volume of a running machine is in use, and a second open
// of it is refused, where "cryptsetup open --test-passphrase" is not.

// initrdAgent is where the module puts the agent inside the initrd.
const initrdAgent = "usr/local/bin/synctang-unlocker"

// What the static checks read, as variables so that tests can say what is there.
var (
	initrdModules = func() ([]string, error) {
		out, err := runCommand(nil, "lsinitrd")
		if err != nil {
			return nil, err
		}
		return parseInitrdModules(string(out))
	}
	installedAgentPath = "/usr/sbin/synctang-unlocker"
	kernelCommandLine  = func() (string, error) {
		raw, err := os.ReadFile("/proc/cmdline")
		return string(raw), err
	}
	staticChecks = staticRehearsalChecks
)

// parseInitrdModules reads the dracut modules from the header lsinitrd prints: the lines after
// "dracut modules:" up to the next rule.
func parseInitrdModules(listing string) ([]string, error) {
	var modules []string
	in := false
	for _, line := range strings.Split(listing, "\n") {
		switch {
		case strings.TrimSpace(line) == "dracut modules:":
			in = true
		case in && strings.HasPrefix(line, "="):
			return modules, nil
		case in && strings.TrimSpace(line) != "":
			modules = append(modules, strings.TrimSpace(line))
		}
	}
	if !in || len(modules) == 0 {
		return nil, errors.New("lsinitrd printed no list of dracut modules")
	}
	return modules, nil
}

// rehearsalCheck is one thing about the initrd. Status is "ok", "problem", or "unchecked" when
// it could not be looked at; Detail says what is wrong and how to fix it, or why it was not
// checked.
type rehearsalCheck struct {
	Name   string
	Status string
	Detail string
}

const (
	identityCheckName = "the initrd carries this machine's identity and network.conf"
	fixInitrd         = "run dracut --force as root, then rehearse again"
)

// staticRehearsalChecks looks at what the boot needs without a key holder: the identity and
// network.conf in the initrd, the dracut modules, the agent in it, and the kernel command line.
func staticRehearsalChecks(identityPath, networkConfPath string) []rehearsalCheck {
	var checks []rehearsalCheck

	st := checkInitrd(identityPath, networkConfPath)
	switch {
	case !st.Checked:
		checks = append(checks, rehearsalCheck{identityCheckName, "unchecked", st.Reason})
	case len(st.Problems) > 0:
		checks = append(checks, rehearsalCheck{identityCheckName, "problem", "the initrd " + strings.Join(st.Problems, "; and it ") + "; " + fixInitrd})
	default:
		checks = append(checks, rehearsalCheck{identityCheckName, "ok", ""})
	}

	const modulesName = "the initrd has the synctang and network dracut modules"
	if mods, err := initrdModules(); err != nil {
		checks = append(checks, rehearsalCheck{modulesName, "unchecked", err.Error()})
	} else {
		var missing []string
		for _, want := range []string{"synctang", "network"} {
			if !slices.Contains(mods, want) {
				missing = append(missing, want)
			}
		}
		if len(missing) > 0 {
			checks = append(checks, rehearsalCheck{modulesName, "problem",
				fmt.Sprintf("the initrd lacks the dracut module(s) %s, so the agent would not be there at boot (is synctang-dracut installed, and dracut-network?); %s", strings.Join(missing, " and "), fixInitrd)})
		} else {
			checks = append(checks, rehearsalCheck{modulesName, "ok", ""})
		}
	}

	checks = append(checks, agentCheck())

	const cmdlineName = "the kernel command line leaves the agent on"
	if cmdline, err := kernelCommandLine(); err != nil {
		checks = append(checks, rehearsalCheck{cmdlineName, "unchecked", err.Error()})
	} else if slices.Contains(strings.Fields(cmdline), "rd.synctang=0") {
		checks = append(checks, rehearsalCheck{cmdlineName, "problem",
			"rd.synctang=0 is on this boot's kernel command line, which switches the agent off in the initrd; take it out of the boot loader entry if it should run"})
	} else {
		checks = append(checks, rehearsalCheck{cmdlineName, "ok", ""})
	}
	return checks
}

// agentCheck compares the agent in the initrd with the installed one: a package upgrade that
// did not rebuild the initrd leaves the old one in it.
func agentCheck() rehearsalCheck {
	const name = "the agent in the initrd is the installed one"
	installed, err := os.ReadFile(installedAgentPath)
	if err != nil {
		return rehearsalCheck{name, "unchecked", err.Error()}
	}
	names, err := initrdListing()
	if err != nil {
		return rehearsalCheck{name, "unchecked", err.Error()}
	}
	if !slices.Contains(names, initrdAgent) {
		return rehearsalCheck{name, "problem", "the initrd has no " + initrdAgent + ", so there would be no agent at boot; " + fixInitrd}
	}
	inside, err := initrdContent(initrdAgent)
	if err != nil {
		return rehearsalCheck{name, "unchecked", err.Error()}
	}
	if !sameAgent(inside, installed) {
		return rehearsalCheck{name, "problem", "the initrd has a different synctang-unlocker from the installed one (the package was upgraded since the initrd was built); " + fixInitrd}
	}
	return rehearsalCheck{name, "ok", ""}
}

// sameAgent reports whether two files are the same program. dracut copies a binary into the
// initrd and may strip it on the way, so the agent there is rarely byte for byte the installed
// file even when it is the same program; what is the same is what gets loaded: the allocated
// sections, by name, address and contents. Stripping takes the symbols and debug information
// and leaves those alone. Anything that is not an ELF file is compared by its bytes.
func sameAgent(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	ha, okA := loadedImageHash(a)
	hb, okB := loadedImageHash(b)
	return okA && okB && ha == hb
}

func loadedImageHash(file []byte) ([32]byte, bool) {
	f, err := elf.NewFile(bytes.NewReader(file))
	if err != nil {
		return [32]byte{}, false
	}
	// One digest per allocated section, put in order of address and name: stripping reorders
	// the section headers, so the order they come in says nothing.
	type record struct {
		addr   uint64
		name   string
		digest [32]byte
	}
	var records []record
	for _, sec := range f.Sections {
		if sec.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		var data []byte
		if sec.Type != elf.SHT_NOBITS { // zeroes in memory, nothing in the file
			if data, err = sec.Data(); err != nil {
				return [32]byte{}, false
			}
		}
		h := sha256.New()
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], sec.Size)
		h.Write(size[:])
		h.Write(data)
		var r record
		r.addr, r.name = sec.Addr, sec.Name
		copy(r.digest[:], h.Sum(nil))
		records = append(records, r)
	}
	if len(records) == 0 {
		return [32]byte{}, false
	}
	slices.SortFunc(records, func(a, b record) int {
		if c := cmp.Compare(a.addr, b.addr); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	h := sha256.New()
	for _, r := range records {
		var addr [8]byte
		binary.LittleEndian.PutUint64(addr[:], r.addr)
		h.Write(addr[:])
		h.Write([]byte(r.name))
		h.Write(r.digest[:])
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, true
}

// printChecks writes the checks and returns how many are problems.
func printChecks(w io.Writer, checks []rehearsalCheck) int {
	problems := 0
	look := ui.For(w)
	for _, c := range checks {
		label := map[string]string{"ok": "ok", "problem": "PROBLEM", "unchecked": "unchecked"}[c.Status]
		if look == (ui.Style{}) {
			// Plain: the columns the output has always had.
			fmt.Fprintf(w, "  %-9s  %s\n", label, c.Name)
			if c.Detail != "" {
				fmt.Fprintf(w, "             %s\n", c.Detail)
			}
		} else {
			mark, name := look.OK(label), c.Name
			switch c.Status {
			case "problem":
				mark, name = look.Fail(label), look.Red(c.Name)
			case "unchecked":
				mark, name = look.Dim("-"), look.Dim(c.Name+" (not checked)")
			}
			fmt.Fprintf(w, "  %s %s\n", mark, name)
			if c.Detail != "" {
				fmt.Fprintf(w, "    %s\n", look.Dim(c.Detail))
			}
		}
		if c.Status == "problem" {
			problems++
		}
	}
	return problems
}

const rehearsalNotTested = `
not tested, because only a real boot can: the network in early boot (DHCP in the initrd) and
the initrd starting at all. If either goes wrong the machine still shows its ordinary
passphrase prompt on the console, and the passphrase still works.
`

// rehearsalHint is what "pair" says once a key holder is enrolled.
func rehearsalHint(device string) string {
	return fmt.Sprintf("to rehearse the unlock without rebooting: synctang-unlocker rehearse --device %s", device)
}

func runRehearse(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rehearse", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	device := fs.String("device", "", "the enrolled LUKS device to rehearse the unlock of (default: the one device with a synctang token)")
	machineName := fs.String("name", "", "this machine's name, shown to the key holder (default: the name given at enrolment)")
	machineKeyFile := fs.String("machine-key-file", defaultMachineKeyFile, "path to this machine's persistent transport identity")
	timeout := fs.Duration("timeout", defaultOpenTimeout, "give up waiting for a key holder after this long")
	initrdOnly := fs.Bool("initrd-only", false, "only check the initrd and the boot settings; wait for no key holder")
	directAddr := fs.String("direct-addr", "", "listen on this address directly, bypassing relay and discovery (testing only)")
	networkConf := fs.String("network-conf", defaultNetworkConfFile, "file naming the relay pool and discovery servers to use instead of the public ones (the kernel arguments rd.synctang.relay= and rd.synctang.discovery= override it)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		ui.Errorf(stderr, "rehearse takes no arguments, got %q\n", fs.Arg(0))
		return 2
	}
	if geteuid() != 0 {
		ui.Errorf(stderr, "rehearse reads the initrd and the LUKS header, which takes root; %s\n", rootHint())
		return 1
	}

	fmt.Fprintln(stdout, "what the boot needs:")
	checks := staticChecks(*machineKeyFile, *networkConf)
	if *machineKeyFile != defaultMachineKeyFile {
		// The initrd only ever carries the default path.
		for i := range checks {
			if checks[i].Name == identityCheckName {
				checks[i] = rehearsalCheck{identityCheckName, "unchecked", *machineKeyFile + " is not the identity file an initrd carries (" + defaultMachineKeyFile + ")"}
			}
		}
	}
	if problems := printChecks(stdout, checks); problems > 0 {
		ui.Errorf(stderr, "%d problem(s) above: fix them first, and nobody was asked to approve a rehearsal that cannot pass\n", problems)
		return 1
	}
	if *initrdOnly {
		fmt.Fprintln(stdout, "\nthe initrd looks right; run rehearse without --initrd-only to try the unlock itself with a key holder")
		return 0
	}

	dev := *device
	if dev == "" {
		found, err := findMr1Devices()
		switch {
		case err != nil:
			ui.Errorf(stderr, "%v; pair a key holder first, or name the device with --device (%s)\n", err, `"synctang-unlocker devices" lists them`)
			return 1
		case len(found) > 1:
			ui.Errorf(stderr, "several devices carry a token (%s): name one with --device\n", strings.Join(found, ", "))
			return 1
		}
		dev = found[0]
	}
	dump, err := runCommand(nil, "cryptsetup", "luksDump", dev)
	if err != nil {
		ui.Errorf(stderr, "reading the LUKS header of %s: %v%s\n", dev, err, permissionHint(err))
		return 1
	}
	if _, id, err := loadToken(string(dump), dev, "", ""); err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	} else if id < 0 {
		ui.Errorf(stderr, "%s has no key holder enrolled, so nobody could approve a rehearsal; pair one first: synctang-unlocker pair --device %s\n", dev, dev)
		return 1
	}
	cert, err := transport.LoadOrCreateCert(*machineKeyFile)
	if err != nil {
		ui.Errorf(stderr, "%v\n", err)
		return 1
	}
	network, ok := resolveNetwork(*networkConf, stdout, stderr)
	if !ok {
		return 1
	}

	fmt.Fprintf(stdout, "\nwaiting for a key holder to approve a rehearsal of unlocking %s\n", dev)
	fmt.Fprintf(stdout, "this machine's transport id: %s\n", protocol.NewDeviceID(cert.Certificate[0]))
	// Nothing is opened: the exchange already checks the recovered secret against a keyslot
	// before it delivers it, and the volume of a running machine is in use.
	nothing := func([]byte) error { return nil }
	if !waitForApproval(dev, *machineName, cert, *timeout, *directAddr, network, nothing, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "\n%s", ui.For(stdout).Green(ui.For(stdout).Emoji("🎉", "")))
	fmt.Fprintf(stdout, "rehearsal passed: a key holder approved, and what it recovered opens %s, which is what the agent checks at boot\n", dev)
	fmt.Fprint(stdout, rehearsalNotTested)
	return 0
}
