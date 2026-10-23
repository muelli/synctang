// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// stallingMachine serves the start of an exchange with script, then
// says nothing more until the test ends: a machine whose relay session
// has died after the phone's half was written, or one that has hung.
func stallingMachine(t *testing.T, addr, phoneSeed, machineSeed string, script func(transport.Conn)) {
	t.Helper()
	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	cert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{
			AuthorizedPeers: []string{phoneID},
			DirectAddr:      addr,
		})
		if err != nil {
			return
		}
		defer conn.Close()
		script(conn)
		<-ctx.Done()
	}()
}

func shortReplyTimeout(t *testing.T) {
	t.Helper()
	saved := replyTimeout
	replyTimeout = 500 * time.Millisecond
	t.Cleanup(func() { replyTimeout = saved })
}

// After the phone relays the confirm code, nothing about the exchange
// waits on a human, so a machine that says nothing has gone wrong.
// The phone used to wait for it for ever, behind a spinner the user
// could not cancel.
func TestSubmitConfirmCodeGivesUpOnASilentMachine(t *testing.T) {
	shortReplyTimeout(t)
	const (
		phoneSeed   = "stall confirm phone seed"
		machineSeed = "stall confirm machine seed"
	)
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	addr := freePort(t)
	stallingMachine(t, addr, phoneSeed, machineSeed, func(conn transport.Conn) {
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "machine"})
		_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: "123456"})
		var resp mrcore.ConfirmCodeResponse
		_ = mrcore.ReadMessage(conn, &resp)
	})

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	start := time.Now()
	err = s.SubmitConfirmCode("123456")
	if err == nil {
		t.Fatal("SubmitConfirmCode reported success against a machine that never answered")
	}
	if elapsed := time.Since(start); elapsed > cancelPrompt {
		t.Fatalf("SubmitConfirmCode took %s", elapsed)
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("error %q does not say the machine went quiet", err)
	}
}

// The same for the verdict after the answer, where the error must also
// keep saying what is known: the answer went out, the outcome did not
// come back, so the machine may or may not have unlocked.
func TestAnswerXOnlyGivesUpOnAMachineThatNeverReportsTheOutcome(t *testing.T) {
	shortReplyTimeout(t)
	v := loadVectors(t)
	const (
		phoneSeed   = "stall verdict phone seed"
		machineSeed = "stall verdict machine seed"
	)
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	addr := freePort(t)
	stallingMachine(t, addr, phoneSeed, machineSeed, func(conn transport.Conn) {
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "machine"})
		_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""})
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{
			Kid: mustHex(t, v.Enrol.Kid),
			X:   mustHex(t, v.Recover.X),
		})
		var resp mrcore.RecoverResponse
		_ = mrcore.ReadMessage(conn, &resp)
	})

	s := NewSession(machineID, phoneSeed)
	s.SetDirectAddr(addr)
	s.SetTimeoutSeconds(30)
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	start := time.Now()
	err = s.AnswerXOnly(make([]byte, 32))
	if err == nil {
		t.Fatal("AnswerXOnly reported success without the machine's verdict")
	}
	if elapsed := time.Since(start); elapsed > cancelPrompt {
		t.Fatalf("AnswerXOnly took %s", elapsed)
	}
	if !strings.Contains(err.Error(), "may or may not have unlocked") {
		t.Errorf("error %q does not say the outcome is unknown", err)
	}
}
