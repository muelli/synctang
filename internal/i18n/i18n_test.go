// SPDX-License-Identifier: AGPL-3.0-or-later

package i18n

import "testing"

func TestTReturnsItsArgumentUnchanged(t *testing.T) {
	for _, s := range []string{"", "plain", "with %s verbs and %% signs\n", "tabs\tand \"quotes\" and ünïcode ✓", "\x1b[31mescapes\x1b[0m"} {
		if got := T(s); got != s {
			t.Errorf("T(%q) = %q", s, got)
		}
	}
}
