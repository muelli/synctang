// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	socket "syncthing-socket"
)

func multicastOrSkip(t *testing.T) {
	t.Helper()
	group := &net.UDPAddr{IP: net.ParseIP("239.255.42.101"), Port: 21078}

	recv, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer recv.Close()

	send, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer send.Close()
	if _, err := send.Write([]byte("probe")); err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
}

// The phone must be able to unlock a machine on the same network with
// no Internet at all: no relay pool, no global discovery, no DNS. This
// is the case the laptop key holder has always handled and the app did
// not, which made "no Internet" mean "no phone unlock" even with both
// devices on the same Wi-Fi.
//
// Deliberately end to end through Session rather than through
// newDialTransport: the point is that the app's own connect path finds
// a machine that is only reachable by multicast, not merely that a
// transport could have been constructed.
func TestSessionConnectsOverLocalDiscoveryWithoutTheRelay(t *testing.T) {
	multicastOrSkip(t)

	const (
		phoneSeed   = "test phone identity seed for local discovery"
		machineSeed = "test machine identity seed for local discovery"
		machineName = "laptop on the same wifi"
	)

	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatalf("DeviceIDForSeed: %v", err)
	}
	machineCert, err := socket.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatalf("machine cert: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	machineErr := make(chan error, 1)
	go func() {
		// LocalDiscovery only: nothing here is announced to global
		// discovery and no relay session exists, so a phone that can
		// only dial over the relay cannot reach this machine at all.
		tr := &transport.LocalDiscovery{Cert: machineCert, AnnounceInterval: 50 * time.Millisecond}
		conn, err := tr.Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}})
		if err != nil {
			machineErr <- err
			return
		}
		defer conn.Close()

		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: machineName}); err != nil {
			machineErr <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""}); err != nil {
			machineErr <- err
			return
		}
		// A recovery request the phone will never be asked to answer:
		// this test is about reachability, and Connect returns once it
		// has read this far.
		if err := mrcore.WriteMessage(conn, mrcore.RecoverRequest{
			Kid: []byte("test-kid"),
			X:   make([]byte, 32),
		}); err != nil {
			machineErr <- err
			return
		}
		machineErr <- nil
	}()

	session := NewSession(machineID, phoneSeed)
	session.SetTimeoutSeconds(25)
	defer session.Close()

	if err := session.Connect(); err != nil {
		t.Fatalf("Connect over local discovery: %v", err)
	}
	if got := session.MachineName(); got != machineName {
		t.Fatalf("MachineName = %q, want %q", got, machineName)
	}

	select {
	case err := <-machineErr:
		if err != nil {
			t.Fatalf("machine side: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("machine side did not finish")
	}
}

// A direct address is an explicit instruction about where to dial, so
// it must not be diluted by racing a multicast search against it: the
// offline tests and the manual same-network pairing that use it are
// relying on exactly one destination being tried. cmd/keyholder's
// newDialTransport draws the same line.
func TestDialTransportUsesLocalDiscoveryOnlyWithoutADirectAddress(t *testing.T) {
	cert, err := socket.GenerateDeterministicCert("test seed for transport shape")
	if err != nil {
		t.Fatalf("GenerateDeterministicCert: %v", err)
	}

	multi, ok := newDialTransport(cert, "").(*transport.Multi)
	if !ok {
		t.Fatalf("with no direct address, got %T, want *transport.Multi", newDialTransport(cert, ""))
	}
	found := false
	for _, tr := range multi.Transports {
		if _, isLocal := tr.(*transport.LocalDiscovery); isLocal {
			found = true
		}
	}
	if !found {
		t.Errorf("the raced transports do not include LocalDiscovery: %#v", multi.Transports)
	}

	if _, isMulti := newDialTransport(cert, "127.0.0.1:1").(*transport.Multi); isMulti {
		t.Error("a direct address should be dialled on its own, not raced against local discovery")
	}
}
