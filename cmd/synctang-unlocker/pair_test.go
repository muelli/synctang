// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
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
	nonce       []byte // the machine's challenge nonce, as dialAndPair last read it
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

	f.nonce = challenge.Nonce
	req := mrcore.PairRequest{
		PubKey: f.holderS,
		Name:   "test phone",
		Proof:  mrcore.PairProof(secret, f.machineID, f.holderID, f.holderS, challenge.Nonce),
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
			confirm:            func(string, []byte, string) bool { return true },
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
			confirm: func(string, []byte, string) bool {
				t.Error("confirmation was asked for despite a bad proof")
				return false
			},
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
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
			confirm: func(peerID string, _ []byte, _ string) bool {
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

// With the passphrase on stdin ("--existing-passphrase-file -", so that it can come from a
// pipe and never touch a disk), stdin is spent by the time the operator is asked whether to
// enrol whoever answered. The question goes to the terminal instead; without one, the
// operator has to say --yes, and is told so before anything waits.

func fakeTTY(t *testing.T, content string, err error) {
	t.Helper()
	was := openTTY
	t.Cleanup(func() { openTTY = was })
	openTTY = func() (io.ReadCloser, error) {
		if err != nil {
			return nil, err
		}
		return io.NopCloser(strings.NewReader(content)), nil
	}
}

func TestConfirmationComesFromTheTerminalWhenStdinHoldsThePassphrase(t *testing.T) {
	fakeTTY(t, "y\n", nil)
	in, closer, err := confirmationInput("-", false, strings.NewReader("passphrase on stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	got, _ := io.ReadAll(in)
	if string(got) != "y\n" {
		t.Errorf("answers were read from %q, want the terminal's", got)
	}
}

func TestConfirmationComesFromStdinWhenItDoesNotHoldThePassphrase(t *testing.T) {
	fakeTTY(t, "from the terminal", nil)
	for _, tc := range []struct {
		file string
		yes  bool
	}{{"/some/file", false}, {"-", true}} {
		in, closer, err := confirmationInput(tc.file, tc.yes, strings.NewReader("from stdin"))
		if err != nil {
			t.Fatal(err)
		}
		closer.Close()
		got, _ := io.ReadAll(in)
		if string(got) != "from stdin" {
			t.Errorf("%+v: answers were read from %q", tc, got)
		}
	}
}

func TestWithNoTerminalAPassphraseOnStdinNeedsYes(t *testing.T) {
	fakeTTY(t, "", errors.New("no such device"))
	if _, _, err := confirmationInput("-", false, strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want one that names --yes", err)
	}
	if _, _, err := confirmationInput("-", true, strings.NewReader("")); err != nil {
		t.Fatalf("with --yes nothing is asked, so no terminal is needed: %v", err)
	}
}

func TestPairSaysSoAtOnceWhenItCannotAskAndWasNotToldYes(t *testing.T) {
	fakeTTY(t, "", errors.New("no such device"))
	var out, errOut bytes.Buffer
	start := time.Now()
	code := run([]string{"pair", "--device", "/dev/x", "--existing-passphrase-file", "-", "--timeout", "30s"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "--yes") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if time.Since(start) > 5*time.Second {
		t.Error("it waited before saying so")
	}
}

// What the operator is asked to compare is the short code derived from this very connection, so
// that the key holder, which can compute the same from what it saw, shows the same digits.
func TestPairShowsTheOperatorTheShortCodeOfThisConnection(t *testing.T) {
	f := newPairFixture(t)
	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	var shown string
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		errCh <- runPairOnce(ctx, pairOptions{
			device: f.device, existingPassphrase: f.passphrase, machineCert: f.machineCert,
			machineName: "test-machine", secret: secret,
			confirm:    func(_ string, _ []byte, sas string) bool { shown = sas; return true },
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		})
	}()
	if result, err := dialAndPair(t, f, f.addr, secret); err != nil || !result.OK {
		t.Fatalf("pairing: %v %+v", err, result)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if want := mrcore.PairSAS(f.machineID, f.holderID, f.holderS, f.nonce); shown != want {
		t.Errorf("the operator was shown %q, the key holder computes %q", shown, want)
	}
}

func TestPairConfirmerPrintsTheCodeAndSaysWhatToCompareItWith(t *testing.T) {
	var out bytes.Buffer
	confirm := pairConfirmer(false, &out, strings.NewReader("y\n"))
	if !confirm("PEER-ID", []byte{4, 1, 2, 3, 4, 5, 6, 7, 8}, "483 291") {
		t.Fatal("y was not taken as yes")
	}
	for _, want := range []string{"483 291", "PEER-ID", "same code"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the question lacks %q:\n%s", want, out.String())
		}
	}
}
