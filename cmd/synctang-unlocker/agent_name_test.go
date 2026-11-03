// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// The dracut hook starts "unlocker agent" with no --name, so the name a key
// holder is shown has to come from the token, where enrolment put it. It used
// to arrive empty: a key holder asked to approve `Machine ""`.
func TestAgentGreetsWithTheNameGivenAtEnrolmentWhenNoneIsPassed(t *testing.T) {
	passphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, passphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatal(err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatal(err)
	}
	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()
	if err := enrolRecipient(device, passphrase, S, "the-enrolled-name", "MACHINE-ID", keyholderID); err != nil {
		t.Fatal(err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		_ = runAgentOnce(ctx, device, machineCert, "", t.TempDir(),
			transport.ListenOptions{DirectAddr: addr}, transport.NewSyncthingRelay(machineCert))
	}()

	conn, err := transport.NewSyncthingRelay(keyholderCert).Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading Hello: %v", err)
	}
	if hello.Name != "the-enrolled-name" {
		t.Errorf("Hello.Name = %q, want the name given at enrolment", hello.Name)
	}
}

// A name passed on the command line still wins, so an operator can override
// what the token says.
func TestAnExplicitAgentNameBeatsTheEnrolledOne(t *testing.T) {
	if got := agentNameFor("from-the-flag", "from-the-token"); got != "from-the-flag" {
		t.Errorf("got %q", got)
	}
	if got := agentNameFor("", "from-the-token"); got != "from-the-token" {
		t.Errorf("got %q", got)
	}
	if got := agentNameFor("", ""); got != "" {
		t.Errorf("got %q", got)
	}
}
