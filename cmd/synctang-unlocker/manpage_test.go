// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

const (
	unlockerPage = "../../man/synctang-unlocker.8"
	networkPage  = "../../man/synctang-network.conf.5"
)

// helpOf is what "synctang-unlocker <command> -h" prints: the real flag set of
// the command, so the man page is held to what the program defines and not to a
// list kept by hand.
func helpOf(t *testing.T, command string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	run([]string{command, "-h"}, &stdout, &stderr)
	return stderr.String() + stdout.String()
}

// Every command of the program has a subsection in COMMANDS, and the options
// listed in it are exactly the flags the command defines.
func TestManPageDocumentsEveryCommandAndFlag(t *testing.T) {
	page := manpagecheck.Page(t, unlockerPage)
	commands := manpagecheck.Commands(usage)
	if len(commands) < 5 {
		t.Fatalf("found only %v in the usage text", commands)
	}
	section, ok := manpagecheck.Section(page, "COMMANDS")
	if !ok {
		t.Fatal("the page has no COMMANDS section")
	}
	for _, command := range commands {
		sub, ok := manpagecheck.SubSection(section, command)
		if !ok {
			t.Errorf("the page has no subsection for the command %q", command)
			continue
		}
		have := manpagecheck.Tags(sub)
		want := manpagecheck.FlagsFromHelp(helpOf(t, command))
		if len(want) == 0 {
			t.Errorf("%s: no flags found in its -h output", command)
		}
		if m := manpagecheck.Missing(want, have); len(m) > 0 {
			t.Errorf("%s: flags the program has and the page does not describe: %v", command, m)
		}
		if m := manpagecheck.Missing(have, want); len(m) > 0 {
			t.Errorf("%s: the page describes flags the program does not have: %v", command, m)
		}
	}
}

// The files, directories, token type and kernel arguments the code defines
// are all named in the pages.
func TestManPagesNameEveryPathAndKernelArgument(t *testing.T) {
	page := manpagecheck.Page(t, unlockerPage)
	network := manpagecheck.Page(t, networkPage)

	for name, want := range map[string]string{
		"machine identity":   defaultMachineKeyFile,
		"network.conf":       defaultNetworkConfFile,
		"ask-password dir":   defaultAskPasswordDir,
		"token type":         mrTokenType,
		"relay argument":     cmdlineRelay,
		"discovery argument": cmdlineDiscovery,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the unlocker page does not mention the %s, %q", name, want)
		}
	}
	for _, want := range []string{defaultNetworkConfFile, cmdlineRelay, cmdlineDiscovery} {
		if !strings.Contains(network, want) {
			t.Errorf("the network.conf page does not mention %q", want)
		}
	}

	// rd.synctang=0 is read by the dracut hook, not by Go.
	hook, err := os.ReadFile("../../dracut/90synctang/synctang-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`getargbool 1 (rd\.[a-z.]+)`).FindSubmatch(hook)
	if m == nil {
		t.Fatal("found no getargbool in the dracut hook")
	}
	if want := string(m[1]) + "=0"; !strings.Contains(page, want) {
		t.Errorf("the unlocker page does not mention %q", want)
	}

	// The value-taking arguments the hook reads through its library (rd.synctang.log=).
	lib, err := os.ReadFile("../../dracut/90synctang/synctang-lib.sh")
	if err != nil {
		t.Fatal(err)
	}
	args := regexp.MustCompile(`getarg (rd\.[a-z.]+=)`).FindAllSubmatch(lib, -1)
	if len(args) == 0 {
		t.Fatal("found no getarg in the dracut library")
	}
	for _, a := range args {
		if !strings.Contains(page, string(a[1])) {
			t.Errorf("the unlocker page does not mention %q", a[1])
		}
	}
	// And the levels it accepts, which are the agent's own.
	for _, level := range []string{"trace", "debug", "info", "warn", "error"} {
		if !strings.Contains(page, level) {
			t.Errorf("the unlocker page does not name the log level %q", level)
		}
	}
	if !strings.Contains(page, "journalctl -b -t synctang-unlocker") {
		t.Error("the unlocker page does not say how to read the agent's log from the journal")
	}
}

// What the page promises about the program's exit status is a list a person
// scripts against, so it must at least say that 2 is misuse.
func TestManPageHasTheSectionsAReaderLooksFor(t *testing.T) {
	page := manpagecheck.Page(t, unlockerPage)
	for _, name := range []string{"NAME", "SYNOPSIS", "DESCRIPTION", "COMMANDS", "EXIT STATUS", "FILES", "KERNEL ARGUMENTS", "ENVIRONMENT", "SECURITY", "EXAMPLES", "SEE ALSO"} {
		if _, ok := manpagecheck.Section(page, name); !ok {
			t.Errorf("the page has no %s section", name)
		}
	}
	// Built in two pieces: written out whole, this line is itself an SPDX tag as
	// far as `reuse lint` can tell, and an invalid one.
	if !strings.Contains(page, ".\\\" SPDX-"+"License-Identifier: AGPL-3.0-or-later") && !strings.HasPrefix(page, ".\\\" SPDX") {
		t.Error("the page has no SPDX header")
	}
}

// Any option the page mentions, anywhere in its text, is one the program has:
// a flag renamed or removed in the code cannot linger in prose or an example.
// Options of other programs that the examples use are listed.
func TestManPageMentionsNoFlagTheProgramLacks(t *testing.T) {
	page := manpagecheck.Page(t, unlockerPage)
	// --force is dracut's, in an example.
	known := map[string]bool{"--force": true}
	for _, command := range manpagecheck.Commands(usage) {
		for _, f := range manpagecheck.FlagsFromHelp(helpOf(t, command)) {
			known[f] = true
		}
	}
	for _, tok := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(page, -1) {
		if !known[tok] {
			t.Errorf("the page mentions %s, which the program does not have", tok)
		}
	}
}
