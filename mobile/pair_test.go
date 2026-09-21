// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	socket "syncthing-socket"
)

// fakeMachine plays the machine's side of a pairing window: challenge,
// verify the proof, answer. Deliberately written against the wire
// messages rather than calling into cmd/unlocker, so that a change to
// either side that breaks the other shows up here.
func fakeMachine(t *testing.T, addr, machineSeed string, secret []byte, accept bool) <-chan mrcore.PairRequest {
	t.Helper()

	cert, err := socket.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}

	got := make(chan mrcore.PairRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		tr := transport.NewSyncthingRelay(cert)
		conn, err := tr.Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			close(got)
			return
		}
		defer conn.Close()

		nonce := []byte("a nonce that is thirty two bytes")
		if err := mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: nonce, Name: "test machine"}); err != nil {
			close(got)
			return
		}

		var req mrcore.PairRequest
		if err := mrcore.ReadMessage(conn, &req); err != nil {
			close(got)
			return
		}

		expected := mrcore.PairProof(secret, machineID, conn.PeerID(), nonce)
		ok := accept && string(expected) == string(req.Proof)
		result := mrcore.PairResult{OK: ok}
		if !ok {
			result.Error = "the pairing code did not match"
		}
		_ = mrcore.WriteMessage(conn, result)
		got <- req
		// Give the phone a moment to read the verdict before the
		// deferred Close tears the connection down under it.
		time.Sleep(500 * time.Millisecond)
	}()
	return got
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// The phone's whole side of pairing: prove the code, hand over the
// public key, and be told it worked.
func TestPairSendsThePublicKeyAndSucceeds(t *testing.T) {
	const (
		phoneSeed   = "test phone seed for pairing"
		machineSeed = "test machine seed for pairing"
	)

	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}

	addr := freePort(t)
	got := fakeMachine(t, addr, machineSeed, secret, true)

	pubKey := hex.EncodeToString([]byte("a public key stands in for a point"))
	if err := Pair(machineID, code, phoneSeed, pubKey, "my phone", addr, 25); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	req, ok := <-got
	if !ok {
		t.Fatal("the machine side never completed")
	}
	if hex.EncodeToString(req.PubKey) != pubKey {
		t.Errorf("machine received public key %x, want %s", req.PubKey, pubKey)
	}
	if req.Name != "my phone" {
		t.Errorf("machine received name %q, want %q", req.Name, "my phone")
	}
}

// A refusal has to come back as a refusal, promptly. Retrying it would
// be pointless (the code cannot become right) and would leave somebody
// watching a spinner until the timeout instead of being told what
// happened.
func TestPairReportsARefusalWithoutRetrying(t *testing.T) {
	const (
		phoneSeed   = "test phone seed for refusal"
		machineSeed = "test machine seed for refusal"
	)

	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}

	addr := freePort(t)
	fakeMachine(t, addr, machineSeed, secret, false)

	start := time.Now()
	err = Pair(machineID, code, phoneSeed, hex.EncodeToString([]byte("key")), "my phone", addr, 60)
	if err == nil {
		t.Fatal("a refused pairing reported success")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("error %q does not say the machine refused", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("a refusal took %s, so it was retried rather than reported", elapsed)
	}
}

// A mistyped pairing code must be caught before anything is dialled,
// so the person retyping it is told immediately rather than after a
// timeout against a machine that was never the problem.
func TestPairRejectsAMalformedCodeWithoutDialling(t *testing.T) {
	err := Pair("MACHINE-ID", "not a valid code", "seed", "00", "phone", "127.0.0.1:1", 5)
	if err == nil {
		t.Fatal("a malformed pairing code was accepted")
	}
	if !strings.Contains(err.Error(), "pairing secret") {
		t.Errorf("error %q does not point at the pairing code", err)
	}
}
