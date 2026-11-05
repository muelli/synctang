// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
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
