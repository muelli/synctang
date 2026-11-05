// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
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
