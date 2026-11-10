// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// What the app shows while it waits is read from Stage, Attempt and Route, which
// never block on a call in progress (Connect holds the session's lock for as long
// as it dials). These tests follow the stages through real exchanges.

// machineScript plays a machine for one unlock: Hello, a request, then the
// verdict once it has an answer. replyDelay holds the verdict back.
func machineScript(t *testing.T, addr, machineSeed, phoneID string) {
	t.Helper()
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}, DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "m"})
		g := mrcore.P256()
		sc, _ := g.RandomScalar()
		pt, _ := g.ScalarBaseMult(sc)
		kid := sha256.Sum256([]byte("a key holder's public point"))
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: kid[:], X: pt})
		var resp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			return
		}
		_ = mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true})
		time.Sleep(300 * time.Millisecond)
	}()
}

func TestSessionStagesAdvanceInOrder(t *testing.T) {
	const phoneSeed, machineSeed = "progress phone seed", "progress machine seed"
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	machineScript(t, addr, machineSeed, phoneID)

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(20)
	defer s.Close()
	if s.Stage() != StageNone || s.Attempt() != 0 {
		t.Fatalf("before Connect: stage %d attempt %d", s.Stage(), s.Attempt())
	}
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	if s.Stage() != StageRequestReady || s.Attempt() != 1 || s.Route() != RouteDirect {
		t.Errorf("after Connect: stage %d attempt %d route %d", s.Stage(), s.Attempt(), s.Route())
	}

	scalar, _ := mrcore.P256().RandomScalar()
	public, _ := PublicPointOf(scalar)
	if err := s.AnswerWithScalar(scalar, public); err != nil {
		t.Fatal(err)
	}

	want := []int{StageStarting, StageConnecting, StageCheckingIdentity, StageWaitingForRequest,
		StageRequestReady, StageSendingAnswer, StageWaitingForVerdict, StageUnlocked}
	if got := s.progress.history(); !reflect.DeepEqual(got, want) {
		t.Errorf("stages %v, want %v", got, want)
	}
}

// A dial that never gets through leaves the stage where the last attempt got to,
// not at the start and not at something it never reached.
func TestAFailedDialLeavesTheStageAtTheLastOneReached(t *testing.T) {
	addr := freePort(t) // nothing listens
	s := NewSession("MACHINE", "seed for a failed dial")
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(1)
	defer s.Close()
	if err := s.Connect(); err == nil {
		t.Fatal("connected to nothing")
	}
	if s.Stage() != StageConnecting {
		t.Errorf("stage %d, want StageConnecting (%d)", s.Stage(), StageConnecting)
	}
	if s.Attempt() < 1 {
		t.Errorf("attempt %d", s.Attempt())
	}
}

// A machine that completes the handshake and then hangs up: the stage is the one
// where it was waiting for the machine to speak.
func TestAHangUpAfterTheHandshakeLeavesTheStageAtWaitingForTheRequest(t *testing.T) {
	const phoneSeed, machineSeed = "hangup phone seed", "hangup machine seed"
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	cert, _ := identity.GenerateDeterministicCert(machineSeed)
	addr := freePort(t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for ctx.Err() == nil {
			conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}, DirectAddr: addr})
			if err == nil {
				conn.Close()
			}
		}
	}()
	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(1)
	defer s.Close()
	if err := s.Connect(); err == nil {
		t.Fatal("connected to a machine that hung up")
	}
	if s.Stage() != StageWaitingForRequest {
		t.Errorf("stage %d, want StageWaitingForRequest (%d)", s.Stage(), StageWaitingForRequest)
	}
}

// Through discovery the route is the one that got there, which the app can
// say. A discovery server that hands back a plain address is a direct route.
func TestSessionReportsTheRouteThatGotThrough(t *testing.T) {
	const phoneSeed, machineSeed = "route phone seed", "route machine seed"
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	machineScript(t, addr, machineSeed, phoneID)
	disco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"addresses": []string{"tcp://" + addr}})
	}))
	defer disco.Close()

	s := NewSession(machineID, phoneSeed)
	if err := s.SetDiscovery(disco.URL + "/v2/?insecure"); err != nil {
		t.Fatal(err)
	}
	s.SetTimeoutSeconds(25)
	defer s.Close()
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	if s.Route() != RouteDirect {
		t.Errorf("route %d, want RouteDirect (%d)", s.Route(), RouteDirect)
	}
	if h := s.progress.history(); len(h) < 3 || h[1] != StageLookingUp {
		t.Errorf("stages %v, want a lookup first", h)
	}
}

func TestPairerStagesAdvanceAndWaitForTheOperator(t *testing.T) {
	const phoneSeed, machineSeed = "progress pair phone", "progress pair machine"
	secret, code, _ := mrcore.NewPairingSecret()
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	got := fakeMachine(t, addr, machineSeed, secret, true)

	p := NewPairer()
	done := make(chan error, 1)
	go func() {
		done <- p.Pair(machineID, code, phoneSeed, hex.EncodeToString([]byte("a public key stands in for a point")), "phone", addr, 25)
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-got

	want := []int{StageStarting, StageConnecting, StageCheckingIdentity, StageSendingProof, StageWaitingForOperator, StageEnrolled}
	if h := p.progress.history(); !reflect.DeepEqual(h, want) {
		t.Errorf("stages %v, want %v", h, want)
	}
	if p.Stage() != StageEnrolled || p.Attempt() != 1 || p.Route() != RouteDirect {
		t.Errorf("stage %d attempt %d route %d", p.Stage(), p.Attempt(), p.Route())
	}
}

// The long wait in pairing is a person at the machine deciding. While they do,
// the stage says so and a poll does not block.
func TestPairerSaysItIsWaitingForTheOperatorWhileTheyDecide(t *testing.T) {
	const phoneSeed, machineSeed = "operator phone", "operator machine"
	secret, code, _ := mrcore.NewPairingSecret()
	machineID, _ := DeviceIDForSeed(machineSeed)
	machineCert, _ := identity.GenerateDeterministicCert(machineSeed)
	addr := freePort(t)
	release := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(machineCert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := machineSide(conn, []byte("a nonce that is thirty two bytes"), "m"); err != nil {
			return
		}
		<-release // the operator is deciding
		_ = mrcore.WriteMessage(conn, mrcore.PairResult{OK: true})
		time.Sleep(300 * time.Millisecond)
	}()
	_ = secret

	p := NewPairer()
	done := make(chan error, 1)
	go func() {
		done <- p.Pair(machineID, code, phoneSeed, hex.EncodeToString([]byte("a public key stands in for a point")), "phone", addr, 25)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for p.Stage() != StageWaitingForOperator {
		if time.Now().After(deadline) {
			t.Fatalf("never reached StageWaitingForOperator, stage %d", p.Stage())
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p.Stage() != StageEnrolled {
		t.Errorf("stage %d after enrolment", p.Stage())
	}
}

// The legs of a raced dial report at once, and a slow leg's early phase must not
// pull the stage back; only a new attempt starts lower.
func TestStageOnlyMovesForwardWithinAnAttempt(t *testing.T) {
	var p progress
	p.beginAttempt(StageLookingUp)
	p.advance(StageCheckingIdentity)
	p.advance(StageLookingUp)
	p.advance(StageConnecting)
	if st, att, _ := p.snapshot(); st != StageCheckingIdentity || att != 1 {
		t.Errorf("stage %d attempt %d", st, att)
	}
	p.beginAttempt(StageLookingUp)
	if st, att, _ := p.snapshot(); st != StageLookingUp || att != 2 {
		t.Errorf("a new attempt: stage %d attempt %d", st, att)
	}
}

// The machine's session has been running since the connection was made, which is
// before its request arrives if it is slow to send it. ConnectedForMillis says how
// long, so the app can count the time to approve from the connection and not from
// the moment Connect returns.
func TestConnectedForMillisCountsFromTheConnectionNotFromTheRequest(t *testing.T) {
	const phoneSeed, machineSeed = "connected phone seed", "connected machine seed"
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	cert, _ := identity.GenerateDeterministicCert(machineSeed)
	addr := freePort(t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}, DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(900 * time.Millisecond) // slow to send anything
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "m"})
		g := mrcore.P256()
		sc, _ := g.RandomScalar()
		pt, _ := g.ScalarBaseMult(sc)
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: []byte("k"), X: pt})
		time.Sleep(500 * time.Millisecond)
	}()

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(20)
	defer s.Close()
	if got := s.ConnectedForMillis(); got != 0 {
		t.Errorf("before Connect: %d", got)
	}
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	got := s.ConnectedForMillis()
	if got < 850 || got > 3000 {
		t.Errorf("ConnectedForMillis = %d after a request that took 900 ms to come", got)
	}
}

// runSessionExchange runs one whole unlock over a direct address against a scripted machine
// and returns the scalar the phone answered with.
func runSessionExchange(t *testing.T, phoneSeed, machineSeed string) []byte {
	t.Helper()
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	machineScript(t, addr, machineSeed, phoneID)
	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(20)
	defer s.Close()
	if err := s.Connect(); err != nil {
		t.Fatal(err)
	}
	scalar, _ := mrcore.P256().RandomScalar()
	public, _ := PublicPointOf(scalar)
	kept := append([]byte(nil), scalar...)
	if err := s.AnswerWithScalar(scalar, public); err != nil {
		t.Fatal(err)
	}
	return kept
}
