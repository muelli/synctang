// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version says which version of a program this is. The version of a package is
// 0.0.<number of commits>-1; the packaging sets Version when it builds (-ldflags "-X"), so that
// "--version" gives the same string as dpkg does.
package version

import (
	"github.com/muelli/synctang/internal/i18n"
	"runtime/debug"
	"strings"
)

// Version is set at build time. It is empty for a build that did not set it.
var Version string

// Describe is what "--version" prints: the program and its version on the first line, then the licence
// and where the source is.
func Describe(program string) string {
	return program + " " + current() + "\n" +
		i18n.T("Licence AGPL-3.0-or-later. Source: https://github.com/muelli/synctang\n")
}

// current is the version, or what is known of a build that was not given one: the commit it was built
// from, when Go recorded one.
func current() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		var revision, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if revision != "" {
			revision = revision[:min(12, len(revision))]
			if modified == "true" {
				revision += ", with changes"
			}
			return i18n.T("(development build, commit ") + strings.TrimSpace(revision) + ")"
		}
	}
	return "(development build)"
}
