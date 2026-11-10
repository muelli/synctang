// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"github.com/muelli/synctang/internal/completion"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

// The completion scripts come from the program's own usage and -h text, so they cannot name
// a command or flag the program lacks or miss one it has. This is the check that it stays so.
func TestCompletionNamesEveryCommandAndFlagTheProgramHas(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"completion", shell}, &out, &errOut); code != 0 {
			t.Fatalf("completion %s: exit %d: %s", shell, code, errOut.String())
		}
		script := out.String()
		if script == "" {
			t.Fatalf("completion %s printed nothing", shell)
		}
		for _, command := range manpagecheck.Commands(usage) {
			if !strings.Contains(script, command) {
				t.Errorf("%s: the script lacks the command %q", shell, command)
			}
			for _, flag := range manpagecheck.FlagsFromHelp(helpOf(t, command)) {
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

func TestCompletionKnowsThatOpenTakesADevice(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"completion", "bash"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "files=1") {
		t.Errorf("open's positional argument is a device, which the script should complete as a path:\n%s", out.String())
	}
}

func TestCompletionNeedsAShell(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"completion"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
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

func TestEveryCommandsHelpFitsATerminal(t *testing.T) {
	for _, command := range []string{"enrol", "agent", "status", "devices", "rehearse", "pair", "open", "completion"} {
		var out, errOut bytes.Buffer
		run([]string{command, "-h"}, &out, &errOut)
		for _, line := range strings.Split(out.String()+errOut.String(), "\n") {
			if len(line) > 78 && strings.Contains(strings.TrimSpace(line), " ") {
				t.Errorf("%s -h: %d columns: %q", command, len(line), line)
			}
		}
	}
}
