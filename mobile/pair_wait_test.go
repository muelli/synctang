// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// A machine whose operator takes their time: it sends the challenge, takes the
// proof, and answers only after decide has returned, or never (decide == nil).
func slowOperatorMachine(t *testing.T, addr, machineSeed string, decide func()) {
	t.Helper()
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: []byte("a nonce that is thirty two bytes"), Name: "m"})
		var req mrcore.PairRequest
		if mrcore.ReadMessage(conn, &req) != nil {
			return
		}
		if decide == nil {
			<-ctx.Done()
			return
		}
		decide()
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{OK: true})
		time.Sleep(300 * time.Millisecond)
	}()
}

func pairKey() string { return hex.EncodeToString([]byte("a public key stands in for a point")) }

// The phone's budget for reaching the machine is not the operator's budget for
// deciding. The operator is a person at a console, asked "enrol it? [y/N]", and
// the machine goes on enrolling after the phone has stopped waiting, so a phone
// that gives up when the dial budget runs out reports failure on a success.
func TestPairWaitsForASlowOperatorLongerThanTheDialBudget(t *testing.T) {
	const phoneSeed, machineSeed = "slow operator phone", "slow operator machine"
	_, code, _ := mrcore.NewPairingSecret()
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	slowOperatorMachine(t, addr, machineSeed, func() { time.Sleep(2500 * time.Millisecond) })

	saved := pairOperatorWait
	pairOperatorWait = 20 * time.Second
	t.Cleanup(func() { pairOperatorWait = saved })

	p := NewPairer()
	// A dial budget of one second; the operator takes two and a half.
	if err := p.Pair(machineID, code, phoneSeed, pairKey(), "phone", addr, 1); err != nil {
		t.Fatalf("Pair: %v (kind %d)", err, p.LastFailure())
	}
	if p.Stage() != StageEnrolled {
		t.Errorf("stage %d", p.Stage())
	}
}

// An operator who never answers ends the wait at the bound, and the failure says
// the machine did not confirm; it never says the machine could not be reached.
func TestPairGivesUpOnAnOperatorWhoNeverAnswersAndSaysSo(t *testing.T) {
	const phoneSeed, machineSeed = "absent operator phone", "absent operator machine"
	_, code, _ := mrcore.NewPairingSecret()
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	slowOperatorMachine(t, addr, machineSeed, nil)

	saved := pairOperatorWait
	pairOperatorWait = 1 * time.Second
	t.Cleanup(func() { pairOperatorWait = saved })

	p := NewPairer()
	start := time.Now()
	err := p.Pair(machineID, code, phoneSeed, pairKey(), "phone", addr, 10)
	if err == nil {
		t.Fatal("paired with a machine that never answered")
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("took %s; the wait is bounded at 1s and must not be retried as a fresh dial", took)
	}
	if p.LastFailure() != FailurePairNotConfirmed {
		t.Errorf("failure kind %d, want FailurePairNotConfirmed (%d); error %v", p.LastFailure(), FailurePairNotConfirmed, err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "reach") {
		t.Errorf("the error says the machine could not be reached: %v", err)
	}
	if p.Stage() != StageWaitingForOperator {
		t.Errorf("stage %d, want the last one reached, StageWaitingForOperator", p.Stage())
	}
}

// Cancel stays available for the whole wait.
func TestPairCanBeCancelledWhileWaitingForTheOperator(t *testing.T) {
	const phoneSeed, machineSeed = "cancel operator phone", "cancel operator machine"
	_, code, _ := mrcore.NewPairingSecret()
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	slowOperatorMachine(t, addr, machineSeed, nil)

	saved := pairOperatorWait
	pairOperatorWait = 30 * time.Second
	t.Cleanup(func() { pairOperatorWait = saved })

	p := NewPairer()
	done := make(chan error, 1)
	go func() { done <- p.Pair(machineID, code, phoneSeed, pairKey(), "phone", addr, 10) }()
	deadline := time.Now().Add(10 * time.Second)
	for p.Stage() != StageWaitingForOperator {
		if time.Now().After(deadline) {
			t.Fatal("never waiting for the operator")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Cancel()
	select {
	case err := <-done:
		if err == nil || p.LastFailure() != FailureCancelled {
			t.Errorf("error %v, kind %d", err, p.LastFailure())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not end the wait")
	}
}

// A real connection failure keeps the old message and kind.
func TestPairStillReportsUnreachableForARealConnectionFailure(t *testing.T) {
	_, code, _ := mrcore.NewPairingSecret()
	p := NewPairer()
	err := p.Pair("MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYC-YLTMRWQ", code, "seed", pairKey(), "phone", freePort(t), 1)
	if err == nil || p.LastFailure() != FailureUnreachable {
		t.Errorf("error %v, kind %d, want FailureUnreachable", err, p.LastFailure())
	}
}
