// SPDX-License-Identifier: AGPL-3.0-or-later

// Package manpagecheck reads the man pages in man/ just far enough for the
// tests that keep them complete: which sections and subsections a page has,
// and which options or keys are the tags of its .TP entries. It is used by
// tests only and understands only the few macros the pages use.
package manpagecheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Page returns the man page at path with its font escapes taken out and its
// hyphen and backslash escapes turned into the characters they print, so a
// test can look for "--device" and "/etc/synctang/machine.pem" as a person
// reads them.
func Page(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the man page is missing: %v", err)
	}
	s := string(raw)
	s = regexp.MustCompile(`\\f(\[[A-Z]*\]|\(..|.)`).ReplaceAllString(s, "")
	s = strings.NewReplacer(`\-`, "-", `\e`, `\`, `\(aq`, "'", `\&`, "").Replace(s)
	return s
}

// Lines of a page between the line that starts with prefix (for example
// ".SH COMMANDS") and the next line that starts with any of stop.
func between(page, prefix string, stop ...string) (string, bool) {
	lines := strings.Split(page, "\n")
	var out []string
	in := false
	for _, l := range lines {
		if in {
			for _, s := range stop {
				if strings.HasPrefix(l, s) {
					return strings.Join(out, "\n"), true
				}
			}
			out = append(out, l)
			continue
		}
		if strings.HasPrefix(l, prefix) {
			in = true
		}
	}
	return strings.Join(out, "\n"), in
}

// Section is the text of ".SH name", up to the next .SH.
func Section(page, name string) (string, bool) {
	return between(page, ".SH "+name, ".SH ")
}

// SubSection is the text of ".SS name", up to the next .SS or .SH.
func SubSection(page, name string) (string, bool) {
	return between(page, ".SS "+name, ".SS ", ".SH ")
}

// Tags returns the first word of the tag line of every .TP entry in text: the
// line after ".TP", with a leading "[" or "<" and anything from the first
// space, "=" or "<" on dropped. For an option's tag that is "--device"; for a
// key's, "relay-pool".
func Tags(text string) []string {
	var tags []string
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, ".TP") || i+1 >= len(lines) {
			continue
		}
		tag := strings.TrimSpace(lines[i+1])
		if end := strings.IndexAny(tag, " =<"); end >= 0 {
			tag = tag[:end]
		}
		tags = append(tags, tag)
	}
	return tags
}

var helpFlag = regexp.MustCompile(`(?m)^ {2}-{1,2}([A-Za-z][A-Za-z0-9-]*)`)

// FlagsFromHelp returns the flag names, written "--name", that the output of
// a flag set's PrintDefaults lists (what "<command> -h" prints).
func FlagsFromHelp(out string) []string {
	var flags []string
	for _, m := range helpFlag.FindAllStringSubmatch(out, -1) {
		flags = append(flags, "--"+m[1])
	}
	return flags
}

// Commands returns the command names in a usage text: the first word of each
// indented line under "commands:" that starts a new entry.
func Commands(usage string) []string {
	var cmds []string
	in := false
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(l, "commands:") {
			in = true
			continue
		}
		if in && strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") {
			if f := strings.Fields(l); len(f) > 0 {
				cmds = append(cmds, f[0])
			}
		}
	}
	return cmds
}

// Missing returns the members of want that are not in have.
func Missing(want, have []string) []string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	var missing []string
	for _, w := range want {
		if !set[w] {
			missing = append(missing, w)
		}
	}
	return missing
}
