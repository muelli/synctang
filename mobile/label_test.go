// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"strings"
	"testing"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

// The machine's name is shown on the phone beside its verified identity.
// It is chosen by the machine, so what reaches the screen must not carry
// control or direction-changing characters, or be unboundedly long.
func TestMachineNameIsMadeSafeToShow(t *testing.T) {
	const (
		phoneSeed   = "test phone identity seed"
		machineSeed = "test machine identity seed"
	)
	addr := freeAddrForTest(t)
	phoneID, _ := DeviceIDForSeed(phoneSeed)
	machineID, _ := DeviceIDForSeed(machineSeed)
	machineCert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}

	hostile := "study\x1b[2J‮evil" + strings.Repeat("x", 500)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(context.Background(), transport.ListenOptions{
			AuthorizedPeers: []string{phoneID},
			DirectAddr:      addr,
		})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: hostile})
		_ = mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: "123456"})
		var resp mrcore.ConfirmCodeResponse
		_ = mrcore.ReadMessage(conn, &resp)
	}()

	session := NewSession(machineID, phoneSeed)
	session.SetDirectAddr(addr)
	session.SetTimeoutSeconds(10)
	defer session.Close()
	if err := session.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	got := session.MachineName()
	if strings.ContainsAny(got, "\x1b‮") {
		t.Fatalf("MachineName carries control or direction characters: %q", got)
	}
	if len([]rune(got)) > machineNameMaxRunes+3 {
		t.Fatalf("MachineName is %d characters long, limit is %d", len([]rune(got)), machineNameMaxRunes)
	}
	if !strings.HasPrefix(got, "study?[2J?evil") {
		t.Fatalf("MachineName = %q, want the readable part kept", got)
	}
}
