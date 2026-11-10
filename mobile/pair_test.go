// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// fakeMachine plays the machine's side of a pairing window: challenge,
// verify the proof, answer. Deliberately written against the wire
// messages rather than calling into cmd/synctang-unlocker, so that a change to
// either side that breaks the other shows up here.
func fakeMachine(t *testing.T, addr, machineSeed string, secret []byte, accept bool) <-chan mrcore.PairRequest {
	t.Helper()

	cert, err := identity.GenerateDeterministicCert(machineSeed)
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
		req, resp, err := machineSide(conn, nonce, "test machine")
		if err != nil {
			close(got)
			return
		}

		expected := mrcore.PairProof(secret, machineID, conn.PeerID(), req.PubKey, nonce)
		ok := accept && string(expected) == string(resp.Proof)
		result := mrcore.PairResult{OK: ok}
		if !ok {
			result.Error = mrcore.PairRefusedCodeMismatch
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

// A phone shows the person the short code while the machine's operator decides, so the code has to
// be there as soon as the request is sent and not only when Pair returns, and has to be the one the
// machine works out from the same connection (mrcore.PairSAS).
func TestPairerOffersTheShortCodeWhileTheOperatorDecides(t *testing.T) {
	const (
		phoneSeed   = "test phone seed for the short code"
		machineSeed = "test machine seed for the short code"
	)
	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	machineID, _ := DeviceIDForSeed(machineSeed)
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineCert, _ := identity.GenerateDeterministicCert(machineSeed)
	addr := freePort(t)
	release := make(chan struct{})
	nonce := []byte("a nonce that is thirty two bytes")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(machineCert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		req, resp, err := machineSide(conn, nonce, "test machine")
		if err != nil {
			return
		}
		<-release // the operator is still deciding
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{OK: string(mrcore.PairProof(secret, machineID, conn.PeerID(), req.PubKey, nonce)) == string(resp.Proof)})
		time.Sleep(500 * time.Millisecond)
	}()

	pubKey := []byte("a public key stands in for a point")
	p := NewPairer()
	if p.ShortCode() != "" {
		t.Errorf("a code before anything was sent: %q", p.ShortCode())
	}
	done := make(chan error, 1)
	go func() { done <- p.Pair(machineID, code, phoneSeed, hex.EncodeToString(pubKey), "my phone", addr, 25) }()

	want := mrcore.PairSAS(machineID, phoneID, pubKey, nonce)
	deadline := time.Now().Add(20 * time.Second)
	for p.ShortCode() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.ShortCode(); got != want {
		t.Errorf("ShortCode = %q while the operator decides, the machine shows %q", got, want)
	}
	if p.Stage() != StageWaitingForOperator {
		t.Errorf("stage %d, want waiting for the operator", p.Stage())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if p.ShortCode() != want {
		t.Errorf("the code was dropped when Pair returned: %q", p.ShortCode())
	}
}

// machineSide is the machine's half of the pairing up to the question to its operator: the key holder asks
// first, the machine answers with a nonce it chooses then, and the key holder proves it has the code.
func machineSide(conn io.ReadWriter, nonce []byte, name string) (mrcore.PairRequest, mrcore.PairResponse, error) {
	var req mrcore.PairRequest
	var resp mrcore.PairResponse
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		return req, resp, err
	}
	if err := mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: nonce, Name: name}); err != nil {
		return req, resp, err
	}
	return req, resp, mrcore.ReadMessage(conn, &resp)
}

// A nonce of a few bytes would let anyone who can pick a key fit it to the short code, so the phone does not
// take one, whatever the machine is.
func TestPairRefusesANonceTooShortToBindAnythingTo(t *testing.T) {
	_, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	machineSeed := "short nonce machine"
	machineID, _ := DeviceIDForSeed(machineSeed)
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	addr := freePort(t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = machineSide(conn, []byte("1234"), "m")
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{OK: true})
		time.Sleep(300 * time.Millisecond)
	}()
	p := NewPairer()
	err = p.Pair(machineID, code, "short nonce phone", hex.EncodeToString([]byte("key")), "phone", addr, 20)
	if err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("a nonce of 4 bytes was taken: %v", err)
	}
	if p.ShortCode() != "" {
		t.Errorf("a code was shown for it: %q", p.ShortCode())
	}
}
