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

// A11: the announcing socket must be bound to a specific interface
// address, never left unbound for the routing table to resolve.
//
// An unbound UDP socket writing to a multicast group needs a route to
// that group, and on an ordinary host the only route that covers
// 239.0.0.0/8 is the default one. So a machine with no default route
// cannot send an announcement at all: the write fails outright with
// "network is unreachable". That is exactly backwards, because a
// machine with no default route is a machine with no Internet, which
// is the one case LocalDiscovery exists to serve. Confirmed on the
// real test VM: with its default route deleted, an unbound socket
// failed with EUNREACH and a socket bound to the interface's own
// address succeeded, on the same host, at the same moment.
//
// Binding to an interface address makes the kernel send out that
// interface without consulting the default route, so this asserts
// what the fix actually depends on rather than the fix's shape.
func TestAnnounceSocketsAreBoundToAnInterfaceAddress(t *testing.T) {
	multicastOrSkip(t)

	l := &LocalDiscovery{Cert: testCert(t)}
	socks := l.announceSockets(context.Background())
	if len(socks) == 0 {
		t.Skip("no multicast-capable interface in this environment")
	}
	t.Cleanup(func() {
		for _, s := range socks {
			s.Close()
		}
	})

	local := make(map[string]bool)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("listing interface addresses: %v", err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if v4 := ipnet.IP.To4(); v4 != nil {
				local[v4.String()] = true
			}
		}
	}

	for _, s := range socks {
		ip := s.LocalAddr().(*net.UDPAddr).IP
		if ip == nil || ip.IsUnspecified() {
			t.Fatalf("announce socket is bound to %v, so sending depends on the routing table", ip)
		}
		if !local[ip.To4().String()] {
			t.Fatalf("announce socket bound to %v, which is not an address of any local interface", ip)
		}
	}
}

// A dialer must join the multicast group on every multicast-capable
// interface, not only whichever one the kernel picks by default. A
// key holder laptop routinely has several (wifi, ethernet, a docker
// bridge), and joining the wrong one means never hearing a machine
// that is announcing perfectly well on the right one.
func TestWaitForAnnouncementJoinsEveryMulticastInterface(t *testing.T) {
	multicastOrSkip(t)

	want := multicastInterfaces()
	if len(want) == 0 {
		t.Skip("no multicast-capable interface in this environment")
	}

	l := &LocalDiscovery{Cert: testCert(t)}
	socks := l.listenSockets()
	if len(socks) != len(want) {
		t.Fatalf("joined the group on %d interfaces, want %d", len(socks), len(want))
	}
	for _, s := range socks {
		s.Close()
	}
}

func deviceIDString(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	return protocol.NewDeviceID(cert.Certificate[0]).String()
}
