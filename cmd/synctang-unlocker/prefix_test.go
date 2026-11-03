// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

// The program is called synctang-unlocker, so that is what its messages and
// its usage say, and what a person is told to type.
func TestMessagesAndUsageUseTheProgramName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"nonsense"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "synctang-unlocker: unknown command") {
		t.Errorf("stderr %q", stderr.String())
	}
	if !strings.Contains(usage, "usage: synctang-unlocker <command>") {
		t.Errorf("usage %q", usage)
	}
	if !strings.Contains(openUsage, "usage: synctang-unlocker open") || !strings.Contains(openUsage, `"synctang-unlocker pair"`) {
		t.Errorf("open usage %q", openUsage)
	}
}
