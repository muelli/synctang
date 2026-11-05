// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/muelli/synctang/internal/manpagecheck"
)

// The man pages write every option with two dashes, and so does everything the program
// prints: the flag package accepts either spelling, but says "-name" unless told otherwise.
func TestEveryCommandsHelpSpellsItsOptionsWithTwoDashes(t *testing.T) {
	single := regexp.MustCompile(`(?m)^  -[A-Za-z]`)
	for _, command := range manpagecheck.Commands(usage) {
		help := helpOf(t, command)
		if single.MatchString(help) {
			t.Errorf("%s -h has an option with one dash:\n%s", command, help)
		}
	}
	if !strings.Contains(helpOf(t, "status"), "  --device string") {
		t.Errorf("status -h:\n%s", helpOf(t, "status"))
	}
}

func TestAnUnknownOptionIsReportedTheGNUWay(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--nope"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unrecognised option '--nope'") || strings.Contains(errOut.String(), "flag provided") {
		t.Errorf("stderr:\n%s", errOut.String())
	}
}
