// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// The key is in the keyring, and the keyring cannot be used: no bus, no service, a login keyring nobody unlocked, a
// password prompt nobody answers. Every command that needs the key says which, in words, and does not say there is
// no key (which would send a person to make a new one, with its new public key, while theirs is only out of reach),
// does not suggest init, and does not dial a machine with nothing to answer with.

var keyringFailures = []struct {
	name string
	raw  string // what go-keyring and godbus say
	want string // what a person is told
}{
	{"no session bus", "dial unix /run/user/1000/bus: connect: no such file or directory", "no D-Bus session"},
	{"dbus-launch missing", `exec: "dbus-launch": executable file not found in $PATH`, "no D-Bus session"},
	{"no Secret Service", "The name org.freedesktop.secrets was not provided by any .service files", "no Secret Service is running"},
	{"the login keyring is locked", "failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'", "locked"},
	{"something else", "rpc error: the keyring shrugged", "the keyring shrugged"},
}

// keyInKeyringThenBroken makes a key in the (mock) keyring and then makes every keyring call fail with raw.
func keyInKeyringThenBroken(t *testing.T, raw string) (keyFile, transportFile string) {
	t.Helper()
	withMockKeyring(t)
	keyFile, transportFile = keyholderDir(t)
	if code, _, e := runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	keyring.MockInitWithError(errors.New(raw))
	return keyFile, transportFile
}

func TestUnlockSaysWhyTheKeyringCannotBeUsedAndDoesNotCallItNoKey(t *testing.T) {
	for _, f := range keyringFailures {
		t.Run(f.name, func(t *testing.T) {
			keyFile, transportFile := keyInKeyringThenBroken(t, f.raw)
			started := time.Now()
			code, out, errOut := runKH("unlock", "--yes", "--timeout", "30s", "--direct-addr", "127.0.0.1:1",
				"--key-file", keyFile, "--transport-key-file", transportFile, machineIDA)
			if code != 1 {
				t.Fatalf("exit %d:\n%s%s", code, out, errOut)
			}
			if !strings.Contains(errOut, "keyring could not be reached") || !strings.Contains(errOut, f.want) {
				t.Errorf("it should say the keyring could not be reached, and why (%q):\n%s", f.want, errOut)
			}
			for _, bad := range []string{"there is no key", "make one with", "synctang-keyholder init", "dialing"} {
				if strings.Contains(errOut, bad) {
					t.Errorf("it says %q, which is wrong or beside the point here:\n%s", bad, errOut)
				}
			}
			if time.Since(started) > 10*time.Second {
				t.Errorf("it kept trying for %s: the key was never going to come", time.Since(started))
			}
		})
	}
}

func TestStatusAndExportPubkeySayWhyTheKeyringCannotBeUsed(t *testing.T) {
	for _, f := range keyringFailures {
		t.Run(f.name, func(t *testing.T) {
			keyFile, transportFile := keyInKeyringThenBroken(t, f.raw)
			code, out, errOut := runKH("status", "--key-file", keyFile, "--transport-key-file", transportFile)
			if code != 0 || !strings.Contains(out, "held by secret-service") || !strings.Contains(out, "readable: no") || !strings.Contains(out, f.want) {
				t.Errorf("status: exit %d:\n%s%s", code, out, errOut)
			}
			if strings.Contains(out+errOut, "there is no key") {
				t.Errorf("status calls it no key:\n%s%s", out, errOut)
			}
			code, out, errOut = runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
			if code != 1 || !strings.Contains(errOut, f.want) || strings.Contains(errOut, "there is no key") {
				t.Errorf("export-pubkey: exit %d:\n%s%s", code, out, errOut)
			}
		})
	}
}

// init does not replace a key that is only out of reach: without --force it refuses, and says why it cannot tell.
func TestInitDoesNotMakeANewKeyBecauseTheKeyringCannotBeReached(t *testing.T) {
	keyFile, transportFile := keyInKeyringThenBroken(t, "dial unix /run/user/1000/bus: connect: no such file or directory")
	was := keyringAvailable
	t.Cleanup(func() { keyringAvailable = was })
	keyringAvailable = func() error { return nil } // the probe passed; the key's own store then fails
	code, _, errOut := runKH("init", "--key-file", keyFile, "--transport-key-file", transportFile)
	if code == 0 || !strings.Contains(errOut, "cannot tell whether a key is already held") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

// A keyring that asks for a password on a screen nobody is looking at never answers. The command says so after
// a while, instead of waiting for ever with nothing on the screen.
func TestAKeyringThatNeverAnswersIsGivenUpOnAndSaid(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	withMockKeyring(t)
	if code, _, e := runKH("init", "--store", "secret-service", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	wasGet, wasTimeout := keyringGet, keyringTimeout
	t.Cleanup(func() { keyringGet, keyringTimeout = wasGet, wasTimeout })
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	keyringGet = func(service, user string) (string, error) { <-block; return "", nil }
	keyringTimeout = 200 * time.Millisecond

	started := time.Now()
	code, _, errOut := runKH("unlock", "--yes", "--timeout", "30s", "--direct-addr", "127.0.0.1:1",
		"--key-file", keyFile, "--transport-key-file", transportFile, machineIDA)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"did not answer", "password prompt", "keyring"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("no %q in:\n%s", want, errOut)
		}
	}
	if time.Since(started) > 5*time.Second {
		t.Errorf("it waited %s", time.Since(started))
	}
}
