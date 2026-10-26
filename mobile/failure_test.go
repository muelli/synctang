// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// The app used to show whatever string a Go error happened to carry,
// "mobile: reading recovery request: EOF" and the like, which says
// nothing to the person holding the phone. LastFailure names what
// happened as one of a fixed set of kinds, so the app can explain it
// in its own words; these tests pin which condition yields which kind.

func assertFailure(t *testing.T, err error, gotKind int, gotDetail string, wantKind int, wantDetail string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a failure, got success")
	}
	if gotKind != wantKind {
		t.Errorf("LastFailure = %d, want %d (error: %v)", gotKind, wantKind, err)
	}
	if gotDetail != wantDetail {
		t.Errorf("LastFailureDetail = %q, want %q", gotDetail, wantDetail)
	}
}

func TestLastFailureOfACancelledConnect(t *testing.T) {
	machineID, _ := DeviceIDForSeed("kind cancel machine")
	s := NewSession(machineID, "kind cancel phone")
	s.SetDirectAddr(freePort(t))
	s.Cancel()
	err := s.Connect()
	assertFailure(t, err, s.LastFailure(), s.LastFailureDetail(), FailureCancelled, "")
}

func TestLastFailureOfAMachineThatCannotBeReached(t *testing.T) {
	machineID, _ := DeviceIDForSeed("kind unreachable machine")
	s := NewSession(machineID, "kind unreachable phone")
	s.SetDirectAddr(freePort(t))
	s.SetTimeoutSeconds(1)
	err := s.Connect()
	assertFailure(t, err, s.LastFailure(), s.LastFailureDetail(), FailureUnreachable, "")
}

// connectedSession dials a machine running script and returns the
// session once Connect has succeeded.
func connectedSession(t *testing.T, name string, script func(transport.Conn)) *Session {
	t.Helper()
	phoneSeed, machineSeed := name+" phone", name+" machine"
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	addr := freePort(t)
	stallingMachine(t, addr, phoneSeed, machineSeed, script)
	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if s.LastFailure() != FailureNone {
		t.Errorf("LastFailure after a successful Connect = %d, want FailureNone", s.LastFailure())
	}
	return s
}

func requestScript(t *testing.T, after func(transport.Conn)) func(transport.Conn) {
	v := loadVectors(t)
	return func(conn transport.Conn) {
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "machine"})
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{
			Kid: mustHex(t, v.Enrol.Kid),
			X:   mustHex(t, v.Recover.X),
		})
		var resp mrcore.RecoverResponse
		_ = mrcore.ReadMessage(conn, &resp)
		after(conn)
	}
}

func TestLastFailureOfAnAnswerWhoseOutcomeNeverArrives(t *testing.T) {
	shortReplyTimeout(t)
	s := connectedSession(t, "kind unknown", requestScript(t, func(transport.Conn) {}))
	err := s.AnswerXOnly(make([]byte, 32))
	assertFailure(t, err, s.LastFailure(), s.LastFailureDetail(), FailureOutcomeUnknown, "")
}

func TestLastFailureOfAnAnswerTheMachineCouldNotUse(t *testing.T) {
	const reason = "recovered secret does not open /dev/vda3"
	s := connectedSession(t, "kind rejected", requestScript(t, func(conn transport.Conn) {
		_ = mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: false, Error: reason})
	}))
	err := s.AnswerXOnly(make([]byte, 32))
	assertFailure(t, err, s.LastFailure(), s.LastFailureDetail(), FailureAnswerRejected, reason)
}

// refusingMachine answers one pairing attempt with a refusal.
func refusingMachine(t *testing.T, addr, machineSeed, reason string) {
	t.Helper()
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	go func() {
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: []byte("a nonce that is thirty two bytes")})
		var req mrcore.PairRequest
		_ = mrcore.ReadMessage(conn, &req)
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{Error: reason})
		time.Sleep(500 * time.Millisecond)
	}()
}

func TestLastFailureOfAPairingRefusal(t *testing.T) {
	for _, tc := range []struct {
		reason     string
		wantKind   int
		wantDetail string
	}{
		{mrcore.PairRefusedCodeMismatch, FailurePairCodeMismatch, ""},
		{mrcore.PairRefusedDeclined, FailurePairDeclined, ""},
		{mrcore.PairRefusedEnrolFailed, FailurePairRefused, mrcore.PairRefusedEnrolFailed},
		{"some reason a newer machine gives", FailurePairRefused, "some reason a newer machine gives"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			_, code, err := mrcore.NewPairingSecret()
			if err != nil {
				t.Fatalf("NewPairingSecret: %v", err)
			}
			machineSeed := "kind refusal machine " + tc.reason
			machineID, _ := DeviceIDForSeed(machineSeed)
			addr := freePort(t)
			refusingMachine(t, addr, machineSeed, tc.reason)

			p := NewPairer()
			err = p.Pair(machineID, code, "kind refusal phone", hex.EncodeToString([]byte("key")), "phone", addr, 20)
			assertFailure(t, err, p.LastFailure(), p.LastFailureDetail(), tc.wantKind, tc.wantDetail)
		})
	}
}

func TestLastFailureOfADamagedPairingCode(t *testing.T) {
	p := NewPairer()
	err := p.Pair("MACHINE-ID", "not a valid code", "seed", "00", "phone", "127.0.0.1:1", 5)
	assertFailure(t, err, p.LastFailure(), p.LastFailureDetail(), FailureBadPairingCode, "")
}

func TestLastFailureOfAPairingThatCannotReachTheMachine(t *testing.T) {
	_, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	machineID, _ := DeviceIDForSeed("kind pair unreachable machine")
	p := NewPairer()
	err = p.Pair(machineID, code, "kind pair unreachable phone", "00", "phone", freePort(t), 1)
	assertFailure(t, err, p.LastFailure(), p.LastFailureDetail(), FailureUnreachable, "")
}

func TestLastFailureOfACancelledPairing(t *testing.T) {
	_, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	p := NewPairer()
	p.Cancel()
	err = p.Pair("MACHINE-ID", code, "seed", "00", "phone", freePort(t), 5)
	assertFailure(t, err, p.LastFailure(), p.LastFailureDetail(), FailureCancelled, "")
}
