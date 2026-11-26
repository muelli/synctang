// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// An initrd built without /etc/synctang/machine.pem boots an agent that makes an identity of its own, which no key holder
// has been shown. The agent said its id and nothing else, so it looked like any other boot; the person at the console
// has to be told that this id is new.
func TestAgentSaysSoWhenItHadToMakeANewIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "synctang", "machine.pem")
	var out, errOut bytes.Buffer
	cert, err := loadAgentIdentity(path, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	id := protocol.NewDeviceID(cert.Certificate[0]).String()
	if !strings.Contains(out.String(), "waiting for a key holder, transport id "+id) {
		t.Errorf("the id is not on the console:\n%s", out.String())
	}
	for _, want := range []string{path, "is new", "no key holder", "rehearse"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("the warning lacks %q:\n%s", want, errOut.String())
		}
	}
}

func TestAgentSaysNothingExtraWhenTheIdentityIsThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.pem")
	cert, err := transport.LoadOrCreateCert(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	got, err := loadAgentIdentity(path, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Errorf("a warning for an identity that was there:\n%s", errOut.String())
	}
	if protocol.NewDeviceID(got.Certificate[0]) != protocol.NewDeviceID(cert.Certificate[0]) {
		t.Error("the agent did not keep the identity it was given")
	}
	if !strings.Contains(out.String(), "waiting for a key holder, transport id ") {
		t.Errorf("the id is not on the console:\n%s", out.String())
	}
}

func TestAgentRefusesAnIdentityFileThatDoesNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if _, err := loadAgentIdentity(path, &out, &errOut); err == nil {
		t.Fatal("a file that is not an identity was taken for one")
	}
	if strings.Contains(errOut.String(), "is new") {
		t.Errorf("an unreadable identity is not a new one:\n%s", errOut.String())
	}
}
