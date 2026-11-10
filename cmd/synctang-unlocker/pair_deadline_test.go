// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A peer that completes the TLS handshake and then never sends its
// pairing request must not hold the pairing window for as long as the
// code lasts: that would let anyone who knows the machine's Device ID
// block a real phone from pairing.
func TestPairGivesUpOnAPeerThatNeverSendsItsRequest(t *testing.T) {
	old := pairRequestTimeout
	pairRequestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { pairRequestTimeout = old })

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	strangerCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "stranger.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()
	addr := freePortForTest(t)

	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runPairOnce(ctx, pairOptions{
			machineCert: machineCert,
			machineName: "test-machine",
			secret:      secret,
			confirm:     func(pairPeer) bool { return true },
			listenOpts:  transport.ListenOptions{DirectAddr: addr},
		})
	}()

	tr := transport.NewSyncthingRelay(strangerCert)
	var conn transport.Conn
	for {
		conn, err = tr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("could not dial the machine: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer conn.Close()

	// Say nothing: not even the request. The machine speaks only after the key holder has, so it has sent
	// nothing, and has to stop waiting on its own.

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("pairing reported success for a peer that never sent a request")
		}
		// What the operator reads while the machine carries on waiting: that a peer said nothing (a phone
		// that was started to unlock, say, while the machine was waiting to pair), not a socket error.
		if !strings.Contains(err.Error(), "did not send its pairing request") || strings.Contains(err.Error(), "closed network connection") {
			t.Errorf("the message is not one a person can act on: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pairing kept waiting for a request that was never going to arrive")
	}
}
