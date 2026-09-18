// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// cryptsetupSearchPath covers where distributions put cryptsetup when
// it is not on the caller's PATH at all: Debian and Ubuntu (including
// the dracut initrd this binary actually runs in) install it to
// /usr/sbin, and some minimal PATHs used for local testing (this
// project's own dev container included) do not carry /usr/sbin.
var cryptsetupSearchPath = []string{"/usr/sbin/cryptsetup", "/sbin/cryptsetup"}

// resolveCommand finds name via the normal PATH lookup first, falling
// back to a short list of known install locations. Only cryptsetup
// needs this; everything else this package execs (truncate, in
// tests) lives on any ordinary PATH.
func resolveCommand(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	if name == "cryptsetup" {
		for _, candidate := range cryptsetupSearchPath {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return name // let exec.Command's own error report the missing binary
}

// runCommand runs name with args, feeding stdin if non-nil, and
// returns stdout. cryptsetup and truncate both write anything useful
// for a human to stderr on failure, so that is folded into the error
// rather than left for the caller to go digging for.
func runCommand(stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(resolveCommand(name), args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%s: %w: %s", name, err, detail)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}
