// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// pairFixture is one machine ready to pair, plus a key holder identity
// standing by to be enrolled.
type pairFixture struct {
	device      string
	passphrase  []byte
	machineCert tls.Certificate
	machineID   string
	holderCert  tls.Certificate
	holderID    string
	holderS     []byte
	holderS_    []byte // the scalar behind holderS
	addr        string
}

func newPairFixture(t *testing.T) *pairFixture {
	t.Helper()

	passphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, passphrase)

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatalf("holder cert: %v", err)
	}

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	return &pairFixture{
		device:      device,
		passphrase:  passphrase,
		machineCert: machineCert,
		machineID:   protocol.NewDeviceID(machineCert.Certificate[0]).String(),
		holderCert:  holderCert,
		holderID:    protocol.NewDeviceID(holderCert.Certificate[0]).String(),
		holderS:     S,
		holderS_:    s,
		addr:        freePortForTest(t),
	}
}

// dialAndPair plays the key holder's side: connect, answer the
// challenge with a proof built from secret, and read the verdict.
func dialAndPair(t *testing.T, f *pairFixture, addr string, secret []byte) (mrcore.PairResult, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The machine may not have bound yet; a direct dial fails fast
	// rather than waiting, so retry until it is there.
	tr := transport.NewSyncthingRelay(f.holderCert)
	var conn transport.Conn
	var err error
	for {
		conn, err = tr.Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: addr})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return mrcore.PairResult{}, err
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer conn.Close()

	var challenge mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		return mrcore.PairResult{}, err
	}

	req := mrcore.PairRequest{
		PubKey: f.holderS,
		Name:   "test phone",
		Proof:  mrcore.PairProof(secret, f.machineID, f.holderID, challenge.Nonce),
	}
	if err := mrcore.WriteMessage(conn, req); err != nil {
		return mrcore.PairResult{}, err
	}

	var result mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		return mrcore.PairResult{}, err
	}
	return result, nil
}

// The whole point of pairing: a key holder that proves it holds the
// secret ends up enrolled, with a keyslot its own key actually opens,
// without anybody copying a public key between two devices by hand.
func TestPairEnrolsAKeyHolderThatProvesTheSecret(t *testing.T) {
	f := newPairFixture(t)

	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		errCh <- runPairOnce(ctx, pairOptions{
			device:             f.device,
			existingPassphrase: f.passphrase,
			machineCert:        f.machineCert,
			machineName:        "test-machine",
			secret:             secret,
			confirm:            func(string, []byte) bool { return true },
			listenOpts:         transport.ListenOptions{DirectAddr: f.addr},
		})
	}()

	result, err := dialAndPair(t, f, f.addr, secret)
	if err != nil {
		t.Fatalf("key holder side: %v", err)
	}
	if !result.OK {
		t.Fatalf("pairing was refused: %s", result.Error)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("runPairOnce: %v", err)
	}

	tok := exportTokenForTest(t, f.device)
	if len(tok.Recipients) != 1 {
		t.Fatalf("expected exactly 1 recipient after pairing, got %d", len(tok.Recipients))
	}
	gotID, ok := tok.Recipients[0].TransportDeviceID()
	if !ok || gotID != f.holderID {
		t.Fatalf("recorded transport id = (%q, %v), want %q", gotID, ok, f.holderID)
	}
	if !opensDeviceForTest(t, f.device, recoverPForTest(t, mrcore.P256(), tok.Recipients[0], f.holderS_)) {
		t.Fatal("the paired key holder's key does not open the device")
	}
}

// A QR code can be photographed, so possession of the secret is the
// only thing standing between a pairing window and a stranger. A peer
// that cannot prove it must not be enrolled, and the device must be
// left exactly as it was.
func TestPairRefusesAKeyHolderWithoutTheSecret(t *testing.T) {
	f := newPairFixture(t)

	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	wrong, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}

	errCh := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	go func() {
		errCh <- runPairOnce(ctx, pairOptions{
			device:             f.device,
			existingPassphrase: f.passphrase,
			machineCert:        f.machineCert,
			machineName:        "test-machine",
			secret:             secret,
			confirm:            func(string, []byte) bool { t.Error("confirmation was asked for despite a bad proof"); return false },
			listenOpts:         transport.ListenOptions{DirectAddr: f.addr},
		})
	}()

	result, err := dialAndPair(t, f, f.addr, wrong)
	if err == nil && result.OK {
		t.Fatal("a key holder with the wrong secret was enrolled")
	}
	if err == nil && !strings.Contains(strings.ToLower(result.Error), "pair") {
		t.Logf("refusal message: %q", result.Error)
	}

	cancel()
	<-errCh

	tok, tokenID, err := loadToken(luksDumpForTest(t, f.device), f.device, "", "")
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if tokenID >= 0 && len(tok.Recipients) != 0 {
		t.Fatalf("a refused pairing still enrolled %d recipients", len(tok.Recipients))
	}
}

// Possession of the secret is not by itself consent: the operator is
// standing at the machine, so the identity being enrolled is shown and
// has to be accepted. Declining must leave the volume untouched.
func TestPairRespectsTheOperatorDeclining(t *testing.T) {
	f := newPairFixture(t)

	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}

	errCh := make(chan error, 1)
	var shownID string
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		errCh <- runPairOnce(ctx, pairOptions{
			device:             f.device,
			existingPassphrase: f.passphrase,
			machineCert:        f.machineCert,
			machineName:        "test-machine",
			secret:             secret,
			confirm: func(peerID string, _ []byte) bool {
				shownID = peerID
				return false
			},
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		})
	}()

	result, err := dialAndPair(t, f, f.addr, secret)
	if err != nil {
		t.Fatalf("key holder side: %v", err)
	}
	if result.OK {
		t.Fatal("pairing succeeded despite the operator declining")
	}
	<-errCh

	if shownID != f.holderID {
		t.Errorf("the operator was shown %q, want the dialling key holder %q", shownID, f.holderID)
	}

	_, tokenID, err := loadToken(luksDumpForTest(t, f.device), f.device, "", "")
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if tokenID >= 0 {
		t.Fatal("a declined pairing still wrote a token")
	}
}

func luksDumpForTest(t *testing.T, device string) string {
	t.Helper()
	dump, err := runCommand(nil, "cryptsetup", "luksDump", device)
	if err != nil {
		t.Fatalf("luksDump: %v", err)
	}
	return string(dump)
}

// freePortForTest returns a loopback address nothing is listening on.
// Racy in principle, but the window is a few microseconds and the
// alternative is a hard-coded port that collides with a parallel test.
func freePortForTest(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
