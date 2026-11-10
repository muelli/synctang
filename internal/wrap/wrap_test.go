// SPDX-License-Identifier: AGPL-3.0-or-later

package wrap

import (
	"strings"
	"testing"
)

func TestParagraphBreaksAtWordsAndIndentsEveryLine(t *testing.T) {
	got := Paragraph("a plain file at the key's path keeps out other users of this laptop and nothing else", "  ", 30)
	want := "  a plain file at the key's\n  path keeps out other users\n  of this laptop and nothing\n  else"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestParagraphKeepsALongWordWhole(t *testing.T) {
	got := Paragraph("see /var/tmp/a/very/long/path/that/does/not/fit/anywhere now", "  ", 20)
	if !strings.Contains(got, "\n  /var/tmp/a/very/long/path/that/does/not/fit/anywhere\n") {
		t.Errorf("the path was broken:\n%s", got)
	}
}

func TestParagraphWithAHangingPrefix(t *testing.T) {
	got := HangingParagraph("WARNING: ", "some words that make a line longer than the width allows", 30)
	for i, line := range strings.Split(got, "\n") {
		if len(line) > 30 {
			t.Errorf("line %d is %d columns: %q", i, len(line), line)
		}
		if i == 0 && !strings.HasPrefix(line, "WARNING: ") {
			t.Errorf("first line %q", line)
		}
		if i > 0 && !strings.HasPrefix(line, "         ") {
			t.Errorf("a continuation under the prefix was expected, got %q", line)
		}
	}
}

func TestParagraphOfNothingIsJustItsIndent(t *testing.T) {
	if got := Paragraph("", "  ", 78); got != "  " {
		t.Errorf("got %q", got)
	}
}

// Width is columns, not bytes: a symbol is several bytes and one column.
func TestParagraphCountsCharactersNotBytes(t *testing.T) {
	got := Paragraph("✓ ✓ ✓ ✓ ✓ ✓ ✓ ✓ ✓ ✓", "", 10)
	if got != "✓ ✓ ✓ ✓ ✓\n✓ ✓ ✓ ✓ ✓" {
		t.Errorf("%q", got)
	}
}
