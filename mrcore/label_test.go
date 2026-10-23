// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeLabel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain text is untouched", "workstation in the study", 64, "workstation in the study"},
		{"non-ASCII letters are kept", "Büro 東京", 64, "Büro 東京"},
		{"terminal escape is neutralised", "\x1b[31mred\x1b[0m", 64, "?[31mred?[0m"},
		{"newline and tab become a visible mark", "a\nb\tc", 64, "a?b?c"},
		{"bidirectional override is neutralised", "evil‮gnp.exe", 64, "evil?gnp.exe"},
		{"zero-width space is neutralised", "a​b", 64, "a?b"},
		{"invalid UTF-8 is neutralised", "a\xffb", 64, "a?b"},
		{"long text is cut at the limit", strings.Repeat("x", 100), 10, "xxxxxxxxxx..."},
		{"cut counts characters, not bytes", strings.Repeat("é", 20), 5, "ééééé..."},
		{"empty stays empty", "", 64, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeLabel(tt.in, tt.max)
			if got != tt.want {
				t.Fatalf("SafeLabel(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("result is not valid UTF-8: %q", got)
			}
		})
	}
}
