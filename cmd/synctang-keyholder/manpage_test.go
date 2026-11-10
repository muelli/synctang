// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

const keyholderPage = "../../man/synctang-keyholder.1"

func helpOf(t *testing.T, command string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	run([]string{command, "-h"}, strings.NewReader(""), &stdout, &stderr)
	return stderr.String() + stdout.String()
}

// Every command has a subsection in COMMANDS, and the options listed in it are
// exactly the flags the command defines, read from the real flag sets.
func TestManPageDocumentsEveryCommandAndFlag(t *testing.T) {
	page := manpagecheck.Page(t, keyholderPage)
	commands := manpagecheck.Commands(usage)
	if len(commands) < 3 {
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
		// A command may have no flags (completion), but its -h must still parse.
		if len(want) == 0 && !strings.Contains(helpOf(t, command), "Usage of "+command) {
			t.Errorf("%s: its -h output has no \"Usage of %s\" line", command, command)
		}
		if m := manpagecheck.Missing(want, have); len(m) > 0 {
			t.Errorf("%s: flags the program has and the page does not describe: %v", command, m)
		}
		if m := manpagecheck.Missing(have, want); len(m) > 0 {
			t.Errorf("%s: the page describes flags the program does not have: %v", command, m)
		}
	}
}

// The directory and the files in it that the code defines, the environment
// variable that moves them, and the other program's name are all in the page.
func TestManPageNamesTheFilesAndTheEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	page := manpagecheck.Page(t, keyholderPage)
	key, _ := defaultKeyFile()
	transportKey, _ := defaultTransportKeyFile()
	config, _ := defaultConfigFile()
	for _, want := range []string{
		configDirName + "/" + filepath.Base(key),
		configDirName + "/" + filepath.Base(transportKey),
		configDirName + "/" + filepath.Base(config),
		"XDG_CONFIG_HOME",
		"synctang-unlocker",
		"synctang-network.conf",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not mention %q", want)
		}
	}
	for _, name := range []string{"NAME", "SYNOPSIS", "DESCRIPTION", "COMMANDS", "EXIT STATUS", "FILES", "ENVIRONMENT", "EXAMPLES", "SEE ALSO"} {
		if _, ok := manpagecheck.Section(page, name); !ok {
			t.Errorf("the page has no %s section", name)
		}
	}
}

// Any option the page mentions, anywhere in its text, is one the program has:
// a flag renamed or removed in the code cannot linger in prose or an example.
// Options of other programs that the examples use are listed.
func TestManPageMentionsNoFlagTheProgramLacks(t *testing.T) {
	page := manpagecheck.Page(t, keyholderPage)
	known := map[string]bool{}
	for _, command := range manpagecheck.Commands(usage) {
		for _, f := range manpagecheck.FlagsFromHelp(helpOf(t, command)) {
			known[f] = true
		}
	}
	// --version is not a flag of any command: it is the whole command line, and the program tests for it.
	known["--version"] = true
	known["--color"] = true
	for _, tok := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(page, -1) {
		if !known[tok] {
			t.Errorf("the page mentions %s, which the program does not have", tok)
		}
	}
}
