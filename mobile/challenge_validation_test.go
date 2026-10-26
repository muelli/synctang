// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"net"
	"testing"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

func freeAddrForTest(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// The phone applies its long-term key to whatever point the machine
// sends, so the point has to be checked when it arrives, before anything
// can reach the Keystore, and not only inside AnswerXOnly after the
// Kotlin side has already used it.
func TestConnectRefusesAnOffCurveChallengePoint(t *testing.T) {
	const (
		phoneSeed   = "test phone identity seed"
		machineSeed = "test machine identity seed"
	)
	addr := freeAddrForTest(t)
	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatal(err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	machineCert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}

	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	offCurve[32] = 1 // x = 1
	offCurve[64] = 1 // y = 1, which is not on P-256 for x = 1

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
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "machine"})
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: make([]byte, 32), X: offCurve})
		var resp mrcore.RecoverResponse
		_ = mrcore.ReadMessage(conn, &resp)
	}()

	session := NewSession(machineID, phoneSeed)
	session.SetDirectAddr(addr)
	session.SetTimeoutSeconds(3)
	defer session.Close()

	if err := session.Connect(); err == nil {
		t.Fatal("Connect accepted a challenge point that is not on the curve")
	}
	if session.HasRequest() {
		t.Fatal("an off-curve challenge point was kept as a request to answer")
	}
	if _, err := session.RequestPointAffineX(); err == nil {
		t.Fatal("RequestPointAffineX handed an off-curve point to the caller")
	}
}
