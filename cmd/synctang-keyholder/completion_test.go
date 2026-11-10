// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"github.com/muelli/synctang/internal/completion"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

func TestCompletionNamesEveryCommandAndFlagTheProgramHas(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"completion", shell}, strings.NewReader(""), &out, &errOut); code != 0 {
			t.Fatalf("completion %s: exit %d: %s", shell, code, errOut.String())
		}
		script := out.String()
		for _, command := range manpagecheck.Commands(usage) {
			if !strings.Contains(script, command) {
				t.Errorf("%s: the script lacks the command %q", shell, command)
			}
			var help bytes.Buffer
			run([]string{command, "-h"}, strings.NewReader(""), &help, &help)
			for _, flag := range manpagecheck.FlagsFromHelp(help.String()) {
				name := strings.TrimPrefix(flag, "--")
				if shell == "fish" {
					if !strings.Contains(script, "-l "+name) {
						t.Errorf("fish: the script lacks the flag %s of %s", flag, command)
					}
				} else if !strings.Contains(script, flag) {
					t.Errorf("%s: the script lacks the flag %s of %s", shell, flag, command)
				}
			}
		}
	}
}

func TestHelpSpellsOptionsWithTwoDashesAndErrorsTheGNUWay(t *testing.T) {
	var help bytes.Buffer
	run([]string{"unlock", "-h"}, strings.NewReader(""), &help, &help)
	if !strings.Contains(help.String(), "  --discovery string") || strings.Contains(help.String(), "\n  -discovery") {
		t.Errorf("unlock -h:\n%s", help.String())
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"unlock", "--nope"}, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unrecognised option '--nope'") {
		t.Errorf("exit %d, stderr:\n%s", code, errOut.String())
	}
}

// The scripts are written once, when the package is built, and installed on other machines, so
// nothing about the machine that built them may be in them: not the home directory that the
// flags' defaults are computed from, which "go build" on one machine and another would
// otherwise make two different packages from the same commit.
func TestCompletionDoesNotEmbedTheHomeDirectoryOfWhoeverBuiltIt(t *testing.T) {
	marker := "/home/the-builder-marker"
	t.Setenv("HOME", marker)
	t.Setenv("XDG_CONFIG_HOME", marker+"/.config")
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"completion", shell}, strings.NewReader(""), &out, &errOut); code != 0 {
			t.Fatalf("%s: exit %d: %s", shell, code, errOut.String())
		}
		if strings.Contains(out.String(), "the-builder-marker") {
			t.Errorf("%s: the script names the builder's home directory:\n%s", shell, out.String())
		}
	}
}

// The usage text is what a person sees first. In a terminal 80 columns wide, a longer line breaks and its
// rest lands at the left margin, under the command names; so every line fits, the descriptions start in
// one column, and a description that needs a second line continues under itself.
func TestUsageFitsATerminalAndAlignsItsDescriptions(t *testing.T) {
	for _, problem := range completion.UsageProblems(usage, 78) {
		t.Error(problem)
	}
}

// Every command's own help fits too, whatever flag gets added: gnuflag wraps the descriptions, and the
// text each command prints before them is written to fit.
func TestEveryCommandsHelpFitsATerminal(t *testing.T) {
	for _, command := range []string{"pair", "init", "export-pubkey", "unlock", "machines", "status", "completion"} {
		var out, errOut bytes.Buffer
		run([]string{command, "-h"}, strings.NewReader(""), &out, &errOut)
		for _, line := range strings.Split(out.String()+errOut.String(), "\n") {
			// A word with no space in it (a path) cannot be wrapped.
			if len(line) > 78 && strings.Contains(strings.TrimSpace(line), " ") {
				t.Errorf("%s -h: %d columns: %q", command, len(line), line)
			}
		}
	}
}
