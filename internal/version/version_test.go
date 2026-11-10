// SPDX-License-Identifier: AGPL-3.0-or-later

package version

import (
	"strings"
	"testing"
)

func TestDescribeNamesTheProgramAndTheVersionItWasBuiltAs(t *testing.T) {
	Version = "0.0.346-1"
	t.Cleanup(func() { Version = "" })
	got := Describe("synctang-keyholder")
	if !strings.HasPrefix(got, "synctang-keyholder 0.0.346-1\n") {
		t.Errorf("first line: %q", got)
	}
	for _, want := range []string{"AGPL-3.0-or-later", "https://github.com/muelli/synctang"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") || strings.Contains(got, "\n\n") {
		t.Errorf("not tidy: %q", got)
	}
}

// A binary that was not built by the packaging has no version to give, and must say so instead of
// passing off a guess as one.
func TestWithoutABuildTimeVersionItSaysItIsADevelopmentBuild(t *testing.T) {
	Version = ""
	got := Describe("synctang-unlocker")
	first, _, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(first, "synctang-unlocker ") || !strings.Contains(first, "development build") {
		t.Errorf("first line: %q", first)
	}
	if strings.Contains(first, "0.0.") {
		t.Errorf("an invented version: %q", first)
	}
}
