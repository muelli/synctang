// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// multicastOrSkip sends a packet to itself on the local discovery
// group and skips the test if it never arrives: some sandboxed CI
// environments restrict multicast even on a machine's own interface,
// and a test asserting on network behaviour that infrastructure does
// not support belongs skipped, not failed.
func multicastOrSkip(t *testing.T) {
	t.Helper()
	group := &net.UDPAddr{IP: net.ParseIP(defaultLocalGroupAddr), Port: defaultLocalPort + 1}

	recv, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer recv.Close()
	recv.SetReadDeadline(time.Now().Add(2 * time.Second))

	send, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer send.Close()
	if _, err := send.Write([]byte("probe")); err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}

	buf := make([]byte, 16)
	if _, _, err := recv.ReadFromUDP(buf); err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
}

// T2.x: a machine (Listen) and a key holder (Dial) on the same LAN
// find each other and complete the identity handshake with no relay
// and no discovery server involved at all, which is what makes unlock
// work without an Internet route.
func TestLocalDiscoveryListenDialRoundTrip(t *testing.T) {
	multicastOrSkip(t)

	machineCert := testCert(t)
	keyholderCert := testCert(t)

	machine := &LocalDiscovery{Cert: machineCert, AnnounceInterval: 50 * time.Millisecond}
	keyholder := &LocalDiscovery{Cert: keyholderCert}

	machineID := deviceIDString(t, machineCert)
	keyholderID := deviceIDString(t, keyholderCert)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type listenResult struct {
		conn Conn
		err  error
	}
	listenCh := make(chan listenResult, 1)
	go func() {
		conn, err := machine.Listen(ctx, ListenOptions{})
		listenCh <- listenResult{conn, err}
	}()

	dialConn, err := keyholder.Dial(ctx, DialOptions{PeerID: machineID})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer dialConn.Close()

	lr := <-listenCh
	if lr.err != nil {
		t.Fatalf("Listen: %v", lr.err)
	}
	defer lr.conn.Close()

	if dialConn.PeerID() != machineID {
		t.Fatalf("dialer's peer = %q, want the machine's id %q", dialConn.PeerID(), machineID)
	}
	if lr.conn.PeerID() != keyholderID {
		t.Fatalf("listener's peer = %q, want the key holder's id %q", lr.conn.PeerID(), keyholderID)
	}
}

// Dial for a Device ID nothing on the LAN is announcing must time out
// via ctx, not hang forever or return a false match.
func TestLocalDiscoveryDialTimesOutWithNoMatchingAnnouncement(t *testing.T) {
	multicastOrSkip(t)

	keyholder := &LocalDiscovery{Cert: testCert(t)}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := keyholder.Dial(ctx, DialOptions{PeerID: "NOBODY-IS-ANNOUNCING-THIS-ID"})
	if err == nil {
		t.Fatal("expected Dial to fail when nobody announces the requested id")
	}
}

// Listen must stop and return ctx's error when no dialer ever
// arrives, exactly like SyncthingRelay's direct listen path.
func TestLocalDiscoveryListenContextCancellation(t *testing.T) {
	multicastOrSkip(t)

	machine := &LocalDiscovery{Cert: testCert(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := machine.Listen(ctx, ListenOptions{})
	if err == nil {
		t.Fatal("expected Listen to fail when ctx is done and nobody ever dials")
	}
}

func deviceIDString(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	return protocol.NewDeviceID(cert.Certificate[0]).String()
}
