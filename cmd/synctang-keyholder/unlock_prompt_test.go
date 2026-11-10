// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// The machine says what it calls itself and proves nothing by it; the name the person gave it in the
// address book is theirs. The prompt shows what is known, and never a pair of empty quotes.
func TestTheApprovalPromptNamesTheMachineWithoutEmptyQuotes(t *testing.T) {
	const id = machineIDA
	for _, tc := range []struct {
		alias, announced string
		want             string
	}{
		{"luks-ubuntu", "", "Machine luks-ubuntu (" + id + ")"},
		{"luks-ubuntu", "luks-ubuntu", "Machine luks-ubuntu (" + id + ")"},
		{"luks-ubuntu", "study-box", `Machine luks-ubuntu (calls itself "study-box", ` + id + ")"},
		{"", "study-box", `Machine "study-box" (` + id + ")"},
		{"", "", "Machine " + id},
	} {
		if got := describeMachine(tc.alias, tc.announced, id); got != tc.want {
			t.Errorf("alias %q, announced %q:\n got %s\nwant %s", tc.alias, tc.announced, got, tc.want)
		}
	}
	if got := describeMachine("", "evil\nname\x1b[31m", machineIDA); strings.ContainsAny(got, "\n\x1b") {
		t.Errorf("a control character got through: %q", got)
	}
}
