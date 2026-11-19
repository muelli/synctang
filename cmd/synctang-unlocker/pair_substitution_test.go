// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muelli/synctang/mobile"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A pairing link that is passed on with another machine's identity in it. The attacker has read the real link
// (a chat message, a clipboard), gets the key holder to use one with the attacker's machine and the real
// pairing secret, and plays both parts: the machine to the key holder, and a key holder, with its own key, to
// the real machine. It sees the key holder's request and the real machine's nonce before it must send the key
// holder its own nonce, and fits that nonce to the code the real machine will show. A review found that this
// made both codes agree and the real machine enrol the attacker's key (2026-11-19).
//
// The unmodified phone library and the unmodified machine are on the two sides. With the key holder's nonce
// committed to before the machine's is sent and revealed after, the attacker cannot know it when it fits, and
// the operator, comparing the codes, sees two different ones and says no.
func TestALinkPassedOnWithAnotherMachineDoesNotMakeTheCodesMatch(t *testing.T) {
	f := newPairFixture(t) // the real machine, with a LUKS image
	secret, encodedPSK, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}

	// The attacker: its own transport identity, used as the fake machine and as a key holder towards the real
	// one, and its own key.
	attackerCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "attacker.pem"))
	if err != nil {
		t.Fatal(err)
	}
	attackerID := protocol.NewDeviceID(attackerCert.Certificate[0]).String()
	g := mrcore.P256()
	sA, _ := g.RandomScalar()
	attackerS, _ := g.ScalarBaseMult(sA)
	attackerAddr := freePortForTest(t)

	// The victim: a phone, through the phone library.
	seedBytes := make([]byte, 32)
	if _, err := rand.Read(seedBytes); err != nil {
		t.Fatal(err)
	}
	victimSeed := hex.EncodeToString(seedBytes)
	victimID, err := mobile.DeviceIDForSeed(victimSeed)
	if err != nil {
		t.Fatal(err)
	}
	sV, _ := g.RandomScalar()
	victimS, _ := g.ScalarBaseMult(sV)
	victim := mobile.NewPairer()

	// The real machine's pairing window. The operator is careful: yes only if the two codes are the same.
	var machineShown, victimShown atomic.Value
	machineDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		machineDone <- runPairOnce(ctx, pairOptions{
			device: f.device, existingPassphrase: f.passphrase, machineCert: f.machineCert,
			machineName: "real-machine", secret: secret,
			confirm: func(p pairPeer) bool {
				machineShown.Store(p.SAS)
				deadline := time.Now().Add(10 * time.Second)
				for victim.ShortCode() == "" && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				victimShown.Store(victim.ShortCode())
				return p.SAS == victim.ShortCode()
			},
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		})
	}()

	// The victim follows the attacker's link: machine = the attacker, secret = the real one.
	victimDone := make(chan error, 1)
	go func() {
		victimDone <- victim.Pair(attackerID, encodedPSK, victimSeed, hex.EncodeToString(victimS), "victims-phone", attackerAddr, 30)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tr := transport.NewSyncthingRelay(attackerCert)

	// 1. As the fake machine: take the victim's request, and hold the challenge back.
	vconn, err := tr.Listen(ctx, transport.ListenOptions{DirectAddr: attackerAddr})
	if err != nil {
		t.Fatal(err)
	}
	defer vconn.Close()
	var vreq mrcore.PairRequest
	if err := mrcore.ReadMessage(vconn, &vreq); err != nil {
		t.Fatal(err)
	}
	if vconn.PeerID() != victimID {
		t.Fatalf("victim id %s, want %s", vconn.PeerID(), victimID)
	}

	// 2. As a key holder towards the real machine: its own key, the victim's name, a proper request. Take the
	// real machine's nonce and the code the operator will be shown.
	var mconn transport.Conn
	for {
		mconn, err = tr.Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: f.addr})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer mconn.Close()
	attacker, err := mrcore.NewPairHolder(secret, f.machineID, attackerID, attackerS, vreq.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := mrcore.WriteMessage(mconn, attacker.Request()); err != nil {
		t.Fatal(err)
	}
	var mch mrcore.PairChallenge
	if err := mrcore.ReadMessage(mconn, &mch); err != nil {
		t.Fatal(err)
	}
	attackerResp, target, err := attacker.Answer(mch)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Fit a nonce for the victim to that code, as well as it can be done without the victim's own nonce,
	// which is not revealed until the victim has had this one.
	unknown := make([]byte, mrcore.PairHolderNonceLen)
	nonce := make([]byte, mrcore.PairNonceLen)
	for tries := 0; ; tries++ {
		if tries > 20_000_000 {
			t.Fatal("could not fit a nonce even to the guess")
		}
		if _, err := rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		if mrcore.PairSAS(attackerID, victimID, vreq.PubKey, nonce, unknown) == target {
			break
		}
	}
	if err := mrcore.WriteMessage(vconn, mrcore.PairChallenge{Nonce: nonce, Name: "real-machine"}); err != nil {
		t.Fatal(err)
	}
	var vresp mrcore.PairResponse
	if err := mrcore.ReadMessage(vconn, &vresp); err != nil {
		t.Fatal(err)
	}

	// 4. Give the real machine its own key holder's response; the operator is asked now.
	if err := mrcore.WriteMessage(mconn, attackerResp); err != nil {
		t.Fatal(err)
	}
	var mres mrcore.PairResult
	if err := mrcore.ReadMessage(mconn, &mres); err != nil {
		t.Fatal(err)
	}
	// 5. Tell the victim what it likes: it is not going to be believed by the machine's operator.
	_ = mrcore.WriteMessage(vconn, mrcore.PairResult{OK: mres.OK})

	<-machineDone
	<-victimDone
	shown, seen := machineShown.Load().(string), victimShown.Load().(string)
	if shown == seen {
		// One in a million by chance, and the attack by design: it is the same code on both sides.
		t.Fatalf("the operator was shown %q and the victim %q: the attacker fitted the code", shown, seen)
	}
	if mres.OK {
		t.Fatal("the machine enrolled the attacker after the operator saw two different codes")
	}
	if plan, _ := planEnrolment(f.device, attackerS, attackerID); plan.Kind != planAdd {
		t.Fatalf("the attacker's key was enrolled: %+v", plan)
	}
}
