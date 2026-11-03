// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

// The program is called synctang-keyholder, so that is what its messages and
// its usage say, and what a person is told to type.
func TestMessagesAndUsageUseTheProgramName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"nonsense"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "synctang-keyholder: unknown command") {
		t.Errorf("stderr %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: synctang-keyholder <command>") {
		t.Errorf("usage %q", stderr.String())
	}
	if !strings.Contains(usage, `"synctang-unlocker enrol"`) {
		t.Errorf("usage should name the other program by its real name: %q", usage)
	}
}
