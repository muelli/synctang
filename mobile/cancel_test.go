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

// cancelPrompt is how long a cancelled call may take to return. Far
// below every timeout these tests set, so a call that merely ran out
// its clock cannot pass for one that was cancelled.
const cancelPrompt = 5 * time.Second

// silentMachine accepts one connection on addr and then says nothing,
// which is what a machine looks like from the phone while a relay
// session is half-dead: dialled, authenticated, and silent.
func silentMachine(t *testing.T, addr, machineSeed string) {
	t.Helper()
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		<-ctx.Done()
		conn.Close()
	}()
}

// runCancelled starts call, cancels it after a moment with cancel, and
// returns what call returned. It fails the test if either call or
// cancel takes longer than cancelPrompt.
func runCancelled(t *testing.T, call func() error, cancel func()) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()

	time.Sleep(500 * time.Millisecond)
	cancelled := make(chan struct{})
	go func() {
		cancel()
		close(cancelled)
	}()
	select {
	case <-cancelled:
	case <-time.After(cancelPrompt):
		t.Fatal("cancelling blocked until the call finished")
	}

	select {
	case err := <-done:
		return err
	case <-time.After(cancelPrompt):
		t.Fatal("the call carried on after being cancelled")
		return nil
	}
}

func assertCancelled(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a cancelled call reported success")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error %q does not say it was cancelled", err)
	}
}

// Nobody is listening, so Connect keeps retrying the dial until its
// timeout. Cancel has to end that at once, not after the timeout.
func TestSessionCancelStopsAConnectThatIsStillDialling(t *testing.T) {
	machineID, err := DeviceIDForSeed("cancel test machine seed")
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	s := NewSession(machineID, "cancel test phone seed")
	s.SetDirectAddr(freePort(t))
	s.SetTimeoutSeconds(60)

	assertCancelled(t, runCancelled(t, s.Connect, s.Cancel))
}

// The dial has succeeded and Connect is blocked reading Hello from a
// machine that never sends it. Reads honour no context of their own,
// so this is the case that needs the connection closed under them.
func TestSessionCancelStopsAConnectWaitingForHello(t *testing.T) {
	const machineSeed = "silent machine seed"
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	addr := freePort(t)
	silentMachine(t, addr, machineSeed)

	s := NewSession(machineID, "cancel test phone seed")
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(60)

	assertCancelled(t, runCancelled(t, s.Connect, s.Cancel))
}

// The app's onCleared calls Close, possibly while a Connect is still
// running on another thread. That must not wait out the dial.
func TestSessionCloseDuringConnectReturnsPromptly(t *testing.T) {
	machineID, err := DeviceIDForSeed("close test machine seed")
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	s := NewSession(machineID, "close test phone seed")
	s.SetDirectAddr(freePort(t))
	s.SetTimeoutSeconds(60)

	assertCancelled(t, runCancelled(t, s.Connect, func() { _ = s.Close() }))
}

// A Cancel that arrives before Connect starts (the user tapped Cancel
// while the call was still being scheduled) must still win.
func TestSessionCancelBeforeConnectPreventsTheDial(t *testing.T) {
	machineID, err := DeviceIDForSeed("early cancel machine seed")
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	s := NewSession(machineID, "early cancel phone seed")
	s.SetDirectAddr(freePort(t))
	s.SetTimeoutSeconds(60)
	s.Cancel()

	start := time.Now()
	assertCancelled(t, s.Connect())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Connect after Cancel took %s", elapsed)
	}
}

// A successful Connect must leave a usable connection behind: the
// cancellation machinery must not close it when Connect returns.
func TestSessionConnectionSurvivesTheEndOfConnect(t *testing.T) {
	v := loadVectors(t)
	const (
		phoneSeed   = "survive test phone seed"
		machineSeed = "survive test machine seed"
	)
	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	addr := freePort(t)

	declined := make(chan string, 1)
	go func() {
		conn, err := transport.NewSyncthingRelay(cert).Listen(context.Background(), transport.ListenOptions{
			AuthorizedPeers: []string{phoneID},
			DirectAddr:      addr,
		})
		if err != nil {
			close(declined)
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "machine"})
		_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""})
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{
			Kid: mustHex(t, v.Enrol.Kid),
			X:   mustHex(t, v.Recover.X),
		})
		var resp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			close(declined)
			return
		}
		declined <- resp.Error
	}()

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()
	// A Cancel after Connect has returned is about an attempt that is
	// over, and must not take the finished connection with it.
	s.Cancel()

	if err := s.Decline("not now"); err != nil {
		t.Fatalf("Decline on a connected session: %v", err)
	}
	select {
	case reason, ok := <-declined:
		if !ok || reason != "not now" {
			t.Errorf("machine received decline %q (ok=%v), want %q", reason, ok, "not now")
		}
	case <-time.After(cancelPrompt):
		t.Fatal("the machine never heard the decline")
	}
}

// Pairing against a machine that is not there retries until its
// timeout; Cancel has to end it at once.
func TestPairerCancelStopsAPairingThatIsStillDialling(t *testing.T) {
	_, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	machineID, err := DeviceIDForSeed("pair cancel machine seed")
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	p := NewPairer()
	addr := freePort(t)
	call := func() error {
		return p.Pair(machineID, code, "pair cancel phone seed", hex.EncodeToString([]byte("key")), "phone", addr, 60)
	}

	assertCancelled(t, runCancelled(t, call, p.Cancel))
}

// The dial succeeded and the machine never sends its challenge.
func TestPairerCancelStopsAPairingWaitingForTheChallenge(t *testing.T) {
	const machineSeed = "silent pairing machine seed"
	_, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	addr := freePort(t)
	silentMachine(t, addr, machineSeed)

	p := NewPairer()
	call := func() error {
		return p.Pair(machineID, code, "pair cancel phone seed", hex.EncodeToString([]byte("key")), "phone", addr, 60)
	}

	assertCancelled(t, runCancelled(t, call, p.Cancel))
}
