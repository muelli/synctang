// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"strings"
)

// hostname is where this machine's own name comes from; a variable so that tests can
// choose it.
var hostname = os.Hostname

// resolveMachineName is the name to show key holders when pairing or enrolling: the one
// given with --name; else the one the volume's token already carries, so that the link and
// the token cannot disagree; else this machine's hostname, which most machines have set to
// something their owner recognises. A hostname that says nothing ("localhost") is no name.
func resolveMachineName(given, existing string) string {
	if given != "" {
		return given
	}
	if existing != "" {
		return existing
	}
	h, err := hostname()
	if err != nil {
		return ""
	}
	switch h = strings.TrimSpace(h); h {
	case "localhost", "localhost.localdomain", "(none)":
		return ""
	}
	return h
}

// existingMachineName is the machine name in device's mr-1 token, or "" when it has none or
// cannot be read.
func existingMachineName(device string) string {
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		return ""
	}
	tok, id, err := loadToken(string(dump), device, "", "")
	if err != nil || id < 0 {
		return ""
	}
	return tok.Machine.Name
}
