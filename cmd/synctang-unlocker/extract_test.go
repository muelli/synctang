// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func (f openFixture) extractArgs(extra ...string) []string {
	return append([]string{"extract-keyslot-key", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr}, extra...)
}

// What a key holder's approval recovers is the passphrase of that key holder's own keyslot. The command prints it,
// and nothing else on standard output, so that $(...) and a pipe take it as it is: it is what "cryptsetup
// --key-file=-" and "--existing-passphrase-file -" read.
func TestExtractKeyslotKeyPrintsThePassphraseOfTheKeyslotThatApprovedAndNothingElse(t *testing.T) {
	fakeOpener(t) // root, and no mapping in the way
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := startOpen(f.extractArgs("--timeout", "20s"))
	if r := f.answer(t, ctx, f.s); !r.OK {
		t.Fatalf("the machine reported failure: %s", r.Error)
	}
	r := waitFor(t, done)

	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if err := verifyPassphrase(f.device, []byte(r.stdout)); err != nil {
		t.Errorf("standard output %q is not a passphrase of the volume: %v", r.stdout, err)
	}
	if strings.ContainsAny(r.stdout, " \n") {
		t.Errorf("standard output has more than the key: %q", r.stdout)
	}
	// Said on standard error, where a pipe does not take it: what this is.
	if !strings.Contains(r.stderr, "unlock") {
		t.Errorf("standard error should say what the key is good for:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, f.machineID) {
		t.Errorf("standard error should name this machine's transport id so a key holder can dial it:\n%s", r.stderr)
	}
}

// An answer that opens nothing is not a key, and a command that was never approved prints nothing.
func TestExtractKeyslotKeyPrintsNothingWhenNobodyApproves(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	r := waitFor(t, startOpen(f.extractArgs("--timeout", "1s")))
	if r.code != 1 || r.stdout != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestExtractKeyslotKeyRefusesWithoutRootBeforeWaitingForAnyone(t *testing.T) {
	fakeOpener(t)
	was := geteuid
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { geteuid = was })
	f := newOpenFixture(t)
	start := time.Now()
	r := waitFor(t, startOpen(f.extractArgs("--timeout", "20s")))
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "root") || time.Since(start) > 5*time.Second {
		t.Errorf("exit %d after %s, stdout %q, stderr %q", r.code, time.Since(start), r.stdout, r.stderr)
	}
}

func TestExtractKeyslotKeyRefusesAVolumeThatHasNoKeyHolder(t *testing.T) {
	fakeOpener(t)
	device := formatLoopbackImage(t, []byte("existing-test-passphrase"))
	r := waitFor(t, startOpen([]string{"extract-keyslot-key", device, "--timeout", "5s"}))
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "no key holder") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

// The command is for people who want to see what is possible, not for the first-time user: the list of
// commands and the shell completion do not name it. The manual page does, and -h on the command itself works.
func TestExtractKeyslotKeyIsLeftOutOfTheUsageAndTheCompletionButHasItsOwnHelp(t *testing.T) {
	if strings.Contains(usage, "extract-keyslot-key") {
		t.Errorf("the usage text names it:\n%s", usage)
	}
	var out, errOut strings.Builder
	if code := run([]string{"completion", "bash"}, &out, &errOut); code != 0 || strings.Contains(out.String(), "extract-keyslot-key") {
		t.Errorf("the completion names it (exit %d)", code)
	}
	help := helpOfCommand("extract-keyslot-key")
	for _, want := range []string{"--timeout", "--machine-key-file", "--direct-addr", "passphrase"} {
		if !strings.Contains(help, want) {
			t.Errorf("-h of the command lacks %q:\n%s", want, help)
		}
	}
}
