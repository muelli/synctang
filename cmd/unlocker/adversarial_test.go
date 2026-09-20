// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// What a key holder can do to the machine.
//
// A key holder is authenticated but not trusted: it is enrolled, so it
// is allowed to talk, but a stolen phone, a compromised laptop, or a
// key holder that is simply broken is still an enrolled key holder.
// The property that has to hold against all of them is that the
// machine never hands systemd anything but the real passphrase, and
// never hangs in a way that leaves it unreachable.

// hostileKeyHolder runs an enrolled peer that answers however the test
// tells it to, and reports what the machine did about it.
func hostileKeyHolder(t *testing.T, answer func(req mrcore.RecoverRequest) mrcore.RecoverResponse) (agentErr error, askAnswered bool) {
	t.Helper()

	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", keyholderID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	askDir := t.TempDir()
	replyConn := writeAskRequest(t, askDir)

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tr := transport.NewSyncthingRelay(machineCert)
	agentErrCh := make(chan error, 1)
	go func() {
		agentErrCh <- runAgentOnce(ctx, device, machineCert, "test-machine", "", askDir,
			transport.ListenOptions{DirectAddr: addr}, tr)
	}()

	keyholderTr := transport.NewSyncthingRelay(keyholderCert)
	conn, err := keyholderTr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("keyholder Dial: %v", err)
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading hello: %v", err)
	}
	var challenge mrcore.ConfirmCodeChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		t.Fatalf("reading confirm code challenge: %v", err)
	}
	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		t.Fatalf("reading recover request: %v", err)
	}

	if err := mrcore.WriteMessage(conn, answer(req)); err != nil {
		t.Fatalf("sending answer: %v", err)
	}

	select {
	case agentErr = <-agentErrCh:
	case <-ctx.Done():
		t.Fatal("the agent neither finished nor failed; it hung on a hostile answer")
	}

	// Did anything reach systemd? The reply socket is the unixgram
	// endpoint the ask-password request names, so anything arriving on
	// it is an answer the agent chose to deliver.
	_ = replyConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 512)
	n, _, _ := replyConn.ReadFrom(buf)
	return agentErr, n > 0
}

// The one that matters: an answer that is well formed but wrong must
// never become a passphrase. verifyPassphrase is what stands between a
// lying key holder and systemd, and it has to run before the answer is
// delivered, not after.
func TestAWrongAnswerNeverReachesSystemd(t *testing.T) {
	g := mrcore.P256()
	err, answered := hostileKeyHolder(t, func(req mrcore.RecoverRequest) mrcore.RecoverResponse {
		// A perfectly valid point, computed with the wrong scalar.
		wrong, scalarErr := g.RandomScalar()
		if scalarErr != nil {
			t.Fatalf("RandomScalar: %v", scalarErr)
		}
		Y, multErr := g.ScalarMult(req.X, wrong)
		if multErr != nil {
			t.Fatalf("ScalarMult: %v", multErr)
		}
		return mrcore.RecoverResponse{Y: Y}
	})
	if err == nil {
		t.Fatal("the agent accepted an answer computed with the wrong key")
	}
	if answered {
		t.Fatal("a wrong answer was delivered to systemd as a passphrase")
	}
}

// A point that is not on the curve at all.
func TestAnOffCurveAnswerIsRefused(t *testing.T) {
	g := mrcore.P256()
	err, answered := hostileKeyHolder(t, func(req mrcore.RecoverRequest) mrcore.RecoverResponse {
		s, _ := g.RandomScalar()
		Y, _ := g.ScalarBaseMult(s)
		bad := append([]byte(nil), Y...)
		bad[len(bad)-1] ^= 0x01
		return mrcore.RecoverResponse{Y: bad}
	})
	if err == nil {
		t.Fatal("the agent accepted a point that is not on the curve")
	}
	if answered {
		t.Fatal("an off-curve answer was delivered to systemd")
	}
}

// Structurally malformed answers: neither field set, both set, junk in
// the x-only field.
func TestMalformedAnswersAreRefused(t *testing.T) {
	cases := map[string]mrcore.RecoverResponse{
		"neither Y nor XOnly": {},
		"empty Y":             {Y: []byte{}},
		"short XOnly":         {XOnly: make([]byte, 16)},
		"long XOnly":          {XOnly: make([]byte, 64)},
		"junk XOnly":          {XOnly: bytes.Repeat([]byte{0xff}, 32)},
		"declared error":      {Error: "no thanks"},
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			err, answered := hostileKeyHolder(t, func(mrcore.RecoverRequest) mrcore.RecoverResponse {
				return resp
			})
			if err == nil {
				t.Fatalf("the agent accepted %s", name)
			}
			if answered {
				t.Fatalf("%s was delivered to systemd", name)
			}
		})
	}
}

var _ = os.Getenv

// A key holder that connects and then says nothing must not strand the
// machine.
//
// This needs no malice to happen: a phone that loses signal between
// dialling and answering, or an app that is killed mid-prompt, leaves
// exactly this. The agent reads the answer with no deadline of its
// own, so without one it waits for the rest of the boot, and the
// machine cannot be unlocked by anybody else in the meantime. The
// person who would fix it is the one standing nowhere near it.
func TestASilentKeyHolderDoesNotStrandTheMachine(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()
	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", keyholderID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	askDir := t.TempDir()
	_ = writeAskRequest(t, askDir)
	addr := freeTestAddr(t)

	// Shortened for the test: what is being checked is that the agent
	// gives up and frees the machine, not how long it waits first.
	restore := exchangeTimeout
	exchangeTimeout = 2 * time.Second
	t.Cleanup(func() { exchangeTimeout = restore })

	ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout+60*time.Second)
	defer cancel()

	tr := transport.NewSyncthingRelay(machineCert)
	agentErrCh := make(chan error, 1)
	go func() {
		agentErrCh <- runAgentOnce(ctx, device, machineCert, "test-machine", "", askDir,
			transport.ListenOptions{DirectAddr: addr}, tr)
	}()

	keyholderTr := transport.NewSyncthingRelay(keyholderCert)
	conn, err := keyholderTr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("keyholder Dial: %v", err)
	}
	// Connect, and then say nothing at all. Deliberately not closing:
	// a phone that has lost signal does not send a FIN either.
	defer conn.Close()

	select {
	case err := <-agentErrCh:
		if err == nil {
			t.Fatal("the agent reported success without ever receiving an answer")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the agent is still waiting on a silent key holder; the machine is stranded until it reboots")
	}
}

// The confirm code defends against somebody holding the key holder
// who cannot see the machine's console, and that same person is the
// one who gets to keep guessing: the code is reused for every
// connection during one agent run. So the comparison must not tell
// them how much of a guess was right.
func TestConfirmCodeComparison(t *testing.T) {
	if !confirmCodeMatches("471902", "471902") {
		t.Error("the right code was rejected")
	}
	for _, wrong := range []string{"", "4", "47190", "471903", "471902 ", "000000", "4719020"} {
		if confirmCodeMatches(wrong, "471902") {
			t.Errorf("%q accepted as the code", wrong)
		}
	}
}
