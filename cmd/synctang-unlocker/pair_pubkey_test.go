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

// The proof vouches for the public key being enrolled. A request that
// carries a different key from the one the proof was made for must be
// refused, so nobody can swap the key in after the proof is made.
func TestPairRefusesARequestWhoseKeyIsNotTheOneTheProofCovers(t *testing.T) {
	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()
	holderID := protocol.NewDeviceID(holderCert.Certificate[0]).String()
	addr := freePortForTest(t)

	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}

	// Two real P-256 public points: the one the proof will cover, and the
	// one the request will actually carry.
	g := mrcore.P256()
	pointFor := func() []byte {
		k, err := g.RandomScalar()
		if err != nil {
			t.Fatal(err)
		}
		p, err := g.ScalarBaseMult(k)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	provedKey, carriedKey := pointFor(), pointFor()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runPairOnce(ctx, pairOptions{
			machineCert: machineCert,
			machineName: "test-machine",
			secret:      secret,
			confirm:     func(string, []byte) bool { return true },
			listenOpts:  transport.ListenOptions{DirectAddr: addr},
		})
	}()

	tr := transport.NewSyncthingRelay(holderCert)
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

	var challenge mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		t.Fatal(err)
	}
	req := mrcore.PairRequest{
		PubKey: carriedKey,
		Name:   "test phone",
		Proof:  mrcore.PairProof(secret, machineID, holderID, provedKey, challenge.Nonce),
	}
	if err := mrcore.WriteMessage(conn, req); err != nil {
		t.Fatal(err)
	}
	var result mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatal("a request carrying a key the proof does not cover was accepted")
	}
	if result.Error != mrcore.PairRefusedCodeMismatch {
		t.Fatalf("refusal reason = %q, want %q", result.Error, mrcore.PairRefusedCodeMismatch)
	}
	if err := <-errCh; err == nil {
		t.Fatal("runPairOnce reported success")
	}
}
