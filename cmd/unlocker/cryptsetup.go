// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// luksKeyMaterial converts p, mrcore's raw recovered (or freshly
// enrolled) secret, into what is actually used as LUKS key material
// and handed to systemd's ask-password protocol: hex, never the raw
// bytes.
//
// Found running unlock against a real deployment: an enrolment whose
// random 32-byte P happened to contain a NUL byte answered
// systemd-ask-password successfully by every measure this code could
// see (verifyPassphrase, which reads raw bytes off stdin, agreed it
// was correct), and cryptsetup still rejected it as an incorrect
// passphrase moments later. systemd's own password handling truncates
// at the first NUL byte somewhere along its path from the ask-password
// socket to cryptsetup; a uniformly random 32-byte secret contains at
// least one roughly one enrolment in eight ((255/256)^32 is not
// negligible), so this was always going to surface, not a fluke of
// this one VM. Hex has no NUL bytes by construction and is applied at
// both ends: enrolRecipient adds the hex form as the actual LUKS2
// keyslot content, and runAgentOnce delivers the hex form, so the
// keyslot and the answer always agree on what "the passphrase" is.
func luksKeyMaterial(p []byte) []byte {
	return []byte(hex.EncodeToString(p))
}

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
