// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import "testing"

func TestClosestFindsATypoAndAPrefix(t *testing.T) {
	names := []string{"luks-ubuntu2604", "fedora", "pair", "unlock"}
	for _, tc := range []struct {
		word, want string
		ok         bool
	}{
		{"fedra", "fedora", true},
		{"unlok", "unlock", true},
		{"luks-ubunt", "luks-ubuntu2604", true}, // a prefix
		{"pairr", "pair", true},
		{"Fedora", "fedora", true}, // case
		{"banana", "", false},
		{"", "", false},
		{"x", "", false},
	} {
		got, ok := Closest(tc.word, names)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%q: %q %v, want %q %v", tc.word, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := Closest("fed", []string{"fedora", "fedora2"}); ok {
		t.Error("a prefix of two names is not a suggestion")
	}
}
