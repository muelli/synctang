// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// Somebody who has the pairing code, and can see the code a key holder shows, can look at the code of each
// connection and leave the ones that are not the one it wants, at no cost, as often as it likes. Each such
// connection is a guess of one in a million. So the machine counts the connections that took a nonce and left
// without a valid answer, and ends the window after a few: the link has leaked, and the operator is told so.

// dialMachine connects as the key holder f.
func dialMachine(t *testing.T, ctx context.Context, f *pairFixture) transport.Conn {
	t.Helper()
	tr := transport.NewSyncthingRelay(f.holderCert)
	for {
		conn, err := tr.Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: f.addr})
		if err == nil {
			return conn
		}
		select {
		case <-ctx.Done():
			t.Fatalf("could not dial the machine: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// takeANonceAndLeave is one guess: a proper request, the nonce, and gone before the answer.
func takeANonceAndLeave(t *testing.T, ctx context.Context, f *pairFixture, secret []byte) {
	t.Helper()
	conn := dialMachine(t, ctx, f)
	defer conn.Close()
	holder, err := mrcore.NewPairHolder(secret, f.machineID, f.holderID, f.holderS, "guesser")
	if err != nil {
		t.Fatal(err)
	}
	if err := mrcore.WriteMessage(conn, holder.Request()); err != nil {
		t.Fatal(err)
	}
	var c mrcore.PairChallenge
	if err := mrcore.ReadMessage(conn, &c); err != nil || len(c.Nonce) == 0 {
		t.Fatalf("no nonce: %v %+v", err, c)
	}
}

func startWindow(t *testing.T, f *pairFixture, secret []byte, limit int, confirm func(pairPeer) bool) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	go func() {
		done <- pairWindow(ctx, pairOptions{
			device: f.device, existingPassphrase: f.passphrase, machineCert: f.machineCert,
			machineName: "test-machine", secret: secret, confirm: confirm,
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		}, limit, func(error) {})
	}()
	return done
}

func TestTheWindowEndsAfterEnoughConnectionsTookANonceAndLeft(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	var asked atomic.Int32
	done := startWindow(t, f, secret, 3, func(pairPeer) bool { asked.Add(1); return false })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		takeANonceAndLeave(t, ctx, f, secret)
	}
	select {
	case err := <-done:
		var many *errTooManyAbandoned
		if !errors.As(err, &many) || many.Count != 3 {
			t.Fatalf("err = %v, want too many abandoned connections, 3", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the window did not end")
	}
	if asked.Load() != 0 {
		t.Errorf("the operator was asked %d times about guesses", asked.Load())
	}
}

// A peer without the code gets no nonce, so there is no code of its to look at, and it does not use up the
// window of the person who does have it.
func TestPeersWithoutThePairingCodeDoNotCountAndTheRealKeyHolderStillPairs(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	done := startWindow(t, f, secret, 3, func(pairPeer) bool { return true })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stranger, _, _ := mrcore.NewPairingSecret()
	for i := 0; i < 5; i++ {
		conn := dialMachine(t, ctx, f)
		holder, _ := mrcore.NewPairHolder(stranger, f.machineID, f.holderID, f.holderS, "")
		_ = mrcore.WriteMessage(conn, holder.Request())
		var c mrcore.PairChallenge
		if err := mrcore.ReadMessage(conn, &c); err != nil || len(c.Nonce) != 0 {
			t.Fatalf("a stranger got %+v (%v)", c, err)
		}
		conn.Close()
	}
	if result, err := dialAndPair(t, f, f.addr, secret); err != nil || !result.OK {
		t.Fatalf("the real key holder: %v %+v", err, result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The operator's no counts too: somebody who has the link can put a question with a fresh code in front of the
// operator as often as it likes, and each is one in a million, seen by the operator, but a tired one says yes.
// A no is what an operator says when the code is not the one on the key holder's screen, which is exactly what
// a leaked link looks like, so the window ends after three.
func TestTheOperatorSayingNoCountsAndTheWindowEndsAfterThree(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	var asked atomic.Int32
	done := startWindow(t, f, secret, 3, func(pairPeer) bool { asked.Add(1); return false })
	for i := 0; i < 3; i++ {
		if result, err := dialAndPair(t, f, f.addr, secret); err != nil || result.OK {
			t.Fatalf("attempt %d: %v %+v", i, err, result)
		}
	}
	select {
	case err := <-done:
		var many *errTooManyAbandoned
		if !errors.As(err, &many) || many.Count != 3 {
			t.Fatalf("err = %v, want too many connections, 3", err)
		}
		if !strings.Contains(err.Error(), "leaked") {
			t.Errorf("the message should say the link has leaked: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the window did not end after three questions the operator declined")
	}
	if asked.Load() != 3 {
		t.Errorf("the operator was asked %d times, want 3", asked.Load())
	}
}

// With nobody to ask, nobody is enrolled: a caller that leaves the question out does not get a pairing that says
// yes to whoever has the code. The command passes an explicit yes for --yes.
func TestAPairingWithNoQuestionToAskTheOperatorEnrolsNobody(t *testing.T) {
	f := newPairFixture(t)
	secret, _, _ := mrcore.NewPairingSecret()
	done := startWindow(t, f, secret, 3, nil)
	result, err := dialAndPair(t, f, f.addr, secret)
	if err != nil || result.OK || result.Error != mrcore.PairRefusedDeclined {
		t.Fatalf("%v %+v, want the pairing declined", err, result)
	}
	if plan, _ := planEnrolment(f.device, f.holderS, f.holderID); plan.Kind != planAdd {
		t.Errorf("the key holder was enrolled: %+v", plan)
	}
	_ = done
}

// What was typed while the machine was still waiting is not the answer to a question that was not on the screen
// yet. The question is printed, then what is queued is dropped, then the answer is read: dropping before it was
// printed would let what is typed while it is being written answer it.
func TestTheQuestionDropsWhatWasTypedUntilItWasOnTheScreenAndThenReads(t *testing.T) {
	var order []string
	was := flushAnswers
	flushAnswers = func() { order = append(order, "flush") }
	t.Cleanup(func() { flushAnswers = was })
	rec := &orderRecorder{log: &order}
	confirm := pairConfirmer(false, rec, bufio.NewReader(rec))
	if !confirm(pairPeer{ID: "PEER", Name: "phone", SAS: "123 456"}) {
		t.Fatal("not confirmed")
	}
	if len(order) < 3 || order[0] != "print" || order[len(order)-2] != "flush" || order[len(order)-1] != "read" {
		t.Errorf("order = %v, want the question printed, then the input dropped, then read", order)
	}
	for i, step := range order {
		if step == "flush" && i != len(order)-2 {
			t.Errorf("the input was dropped before the question was printed: %v", order)
		}
	}
}

// orderRecorder records what the question does, in order, as writer and as reader.
type orderRecorder struct {
	log  *[]string
	done bool
}

func (w *orderRecorder) Write(p []byte) (int, error) {
	if n := len(*w.log); n == 0 || (*w.log)[n-1] != "print" {
		*w.log = append(*w.log, "print")
	}
	return len(p), nil
}

func (w *orderRecorder) Read(p []byte) (int, error) {
	*w.log = append(*w.log, "read")
	if w.done {
		return 0, io.EOF
	}
	w.done = true
	return copy(p, "y\n"), nil
}
