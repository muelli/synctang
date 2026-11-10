// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// What makes the short code mean something is the order of the messages: the key holder says which key it
// wants enrolled, and only then does the machine choose the nonce the code is made from. A peer that could see the
// nonce before it chose its key could grind the key to any code it liked.

// startPairingWindow runs one pairing window in the background and returns what the operator was shown.
func startPairingWindow(t *testing.T, f *pairFixture, secret []byte, answer bool) (shown chan pairPeer, done chan error) {
	t.Helper()
	shown, done = make(chan pairPeer, 1), make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- runPairOnce(ctx, pairOptions{
			device: f.device, existingPassphrase: f.passphrase, machineCert: f.machineCert,
			machineName: "test-machine", secret: secret,
			confirm:    func(p pairPeer) bool { shown <- p; return answer },
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		})
	}()
	return shown, done
}

func TestTheMachineSendsNoNonceUntilTheKeyHolderHasSaidWhichKeyItWants(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	_, done := startPairingWindow(t, f, secret, false)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr := transport.NewSyncthingRelay(f.holderCert)
	var conn transport.Conn
	var err error
	for {
		if conn, err = tr.Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: f.addr}); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()

	got := make(chan mrcore.PairChallenge, 1)
	go func() {
		var c mrcore.PairChallenge
		if mrcore.ReadMessage(conn, &c) == nil {
			got <- c
		}
	}()
	select {
	case c := <-got:
		t.Fatalf("the machine sent a nonce (%x) before it knew which key was wanted", c.Nonce)
	case <-time.After(400 * time.Millisecond):
	}
	if err := mrcore.WriteMessage(conn, mrcore.PairRequest{PubKey: f.holderS}); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-got:
		if len(c.Nonce) < mrcore.PairMinNonceLen {
			t.Errorf("a nonce of %d bytes", len(c.Nonce))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no nonce after the request")
	}
	conn.Close()
	<-done
}

func TestAKeyThatCanNeverWorkIsRefusedBeforeAnyNonceIsSent(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	_, done := startPairingWindow(t, f, secret, true)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr := transport.NewSyncthingRelay(f.holderCert)
	var conn transport.Conn
	var err error
	for {
		if conn, err = tr.Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: f.addr}); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()
	if err := mrcore.WriteMessage(conn, mrcore.PairRequest{PubKey: bytes.Repeat([]byte{0xff}, 65)}); err != nil {
		t.Fatal(err)
	}
	var first mrcore.PairResult
	if err := mrcore.ReadMessage(conn, &first); err != nil {
		t.Fatal(err)
	}
	if first.OK || first.Error != mrcore.PairRefusedInvalidKey {
		t.Errorf("the first message back was %+v, want the refusal of the key", first)
	}
	if err := <-done; err == nil {
		t.Error("runPairOnce reported success")
	}
}

// Two connections with the same key holder and the same key do not show the same code: the nonce is the machine's,
// fresh for each, so the code of one connection says nothing of another's.
func TestTheCodeOfOneConnectionIsNotTheCodeOfAnother(t *testing.T) {
	f := newPairFixture(t)
	var codes []string
	for i := 0; i < 2; i++ {
		secret, _, _ := mrcore.NewPairingSecret()
		shown, done := startPairingWindow(t, f, secret, false)
		if result, err := dialAndPair(t, f, f.addr, secret); err != nil || result.OK {
			t.Fatalf("pairing %d: %v %+v", i, err, result)
		}
		p := <-shown
		codes = append(codes, p.SAS)
		// What the key holder works out from the nonce it was sent is what the operator was shown.
		if want := mrcore.PairSAS(f.machineID, f.holderID, f.holderS, f.nonce); p.SAS != want {
			t.Errorf("the operator was shown %q, the key holder computes %q", p.SAS, want)
		}
		<-done
	}
	if codes[0] == codes[1] {
		t.Errorf("the same code %q for two connections", codes[0])
	}
}

// What the operator is shown of a key holder: its Device ID, the id of the key (the same id that status lists, a hash
// of the key and not the first bytes of it), what it calls itself with nothing in it that a terminal would act
// on, and the code.
func TestThePairingQuestionShowsTheKeyIDTheNameAndTheCode(t *testing.T) {
	pub := []byte{4, 1, 2, 3, 4, 5, 6, 7, 8}
	kid := sha256.Sum256(pub)
	var out bytes.Buffer
	ok := pairConfirmer(false, &out, strings.NewReader("y\n"))(pairPeer{
		ID: "PEER-ID", PubKey: pub, Name: mrcore.SafeLabel("laptop\x1b[2J\nforged line", 64), SAS: "483 291",
	})
	if !ok {
		t.Fatal("y was not yes")
	}
	got := out.String()
	for _, want := range []string{"PEER-ID", "key id:        " + shortKid(kid[:]), "483 291", "it calls itself: laptop"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b") || strings.Contains(got, "\nforged line") {
		t.Errorf("a name got a terminal to act on it:\n%q", got)
	}
}

// A key holder that is already enrolled and comes back with a new key is shown the code too: the check against a
// leaked link is not for new key holders only.
func TestTheReplaceQuestionShowsTheCodeToCompare(t *testing.T) {
	var out bytes.Buffer
	old := existingRecipient{Keyslot: "3", Kid: kidOf([]byte("old"))}
	pairReplaceConfirmer(false, &out, strings.NewReader("n\n"))(pairPeer{ID: testKH1, PubKey: []byte{4, 1}, SAS: "123 456"}, old, kidOf([]byte("new")))
	for _, want := range []string{"123 456", "same code", testKH1} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
}

// "Good once" said less than it should: a wrong code does not use the code up, and what ends it is one key
// holder being enrolled, or the window.
func TestThePairingInstructionsSayWhatEndsTheCode(t *testing.T) {
	text := pairingInstructions("MACHINE", "box", "SECRET", transport.NetworkConfig{}, 5*time.Minute)
	if strings.Contains(text, "good once") {
		t.Errorf("the old wording is back:\n%s", text)
	}
	if !strings.Contains(text, "until one key holder is enrolled or 5m0s are over") {
		t.Errorf("it does not say what ends the code:\n%s", text)
	}
}

// --yes gives up the one check that a leaked link is caught by, and its own description says so.
func TestYesSaysItGivesUpTheCodeCheck(t *testing.T) {
	var out, errOut bytes.Buffer
	run([]string{"pair", "-h"}, &out, &errOut)
	help := strings.Join(strings.Fields(out.String()+errOut.String()), " ")
	if !strings.Contains(help, "skips the code check") {
		t.Errorf("--yes does not say what it skips:\n%s", help)
	}
}
