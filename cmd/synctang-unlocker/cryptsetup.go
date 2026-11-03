// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
// secretFDPath is where the child finds the nth secret handed to it.
// exec.Cmd.ExtraFiles places the first entry at descriptor 3.
func secretFDPath(n int) string {
	return fmt.Sprintf("/proc/self/fd/%d", 3+n)
}

// runCommandWithSecrets runs name with each secret on a file
// descriptor of its own, and asks buildArgs for the arguments given
// the paths the child should read them from.
//
// The point is what it avoids. cryptsetup can read two secrets from
// one stdin stream, but then it has to be told where the first ends,
// and --keyfile-size=N puts the length of the operator's LUKS
// passphrase into argv, where any local process can read it out of
// /proc/<pid>/cmdline for as long as the command runs. The value was
// never exposed, but the length narrows a search, and there is no
// reason to disclose it. One descriptor per secret means cryptsetup
// reads each to EOF and no size has to be named at all.
//
// Nothing touches the filesystem: these are pipes, and the paths are
// /proc/self/fd entries pointing at them.
func runCommandWithSecrets(secrets [][]byte, name string, buildArgs func(paths []string) []string) ([]byte, error) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		return nil, fmt.Errorf("%s: passing secrets needs /proc mounted: %w", name, err)
	}

	paths := make([]string, len(secrets))
	readEnds := make([]*os.File, len(secrets))
	writeEnds := make([]*os.File, len(secrets))
	defer func() {
		for _, f := range readEnds {
			if f != nil {
				f.Close()
			}
		}
	}()

	for i := range secrets {
		r, w, err := os.Pipe()
		if err != nil {
			for _, f := range writeEnds {
				if f != nil {
					f.Close()
				}
			}
			return nil, fmt.Errorf("%s: creating a pipe for a secret: %w", name, err)
		}
		readEnds[i], writeEnds[i] = r, w
		paths[i] = secretFDPath(i)
	}

	cmd := exec.Command(resolveCommand(name), buildArgs(paths)...)
	cmd.ExtraFiles = readEnds
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		for _, f := range writeEnds {
			f.Close()
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	// Written after Start, from goroutines, so that a secret larger
	// than the pipe buffer cannot deadlock against a child that has
	// not started reading yet.
	var wg sync.WaitGroup
	for i, w := range writeEnds {
		wg.Add(1)
		go func(w *os.File, secret []byte) {
			defer wg.Done()
			defer w.Close()
			_, _ = w.Write(secret)
		}(w, secrets[i])
	}
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%s: %w: %s", name, err, detail)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

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
