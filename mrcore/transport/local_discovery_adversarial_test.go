// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Local discovery listens to unauthenticated UDP from anybody on the
// network. That is unavoidable, and safe as far as secrets go, because
// an announcement is only ever a hint about where to dial and the TLS
// handshake afterwards pins the Device ID. What it is not automatically
// safe against is denial of service: a neighbour who announces the
// machine's Device ID pointing at nothing can send the key holder
// dialling into a hole.
//
// That matters more here than it looks. Local discovery is the path
// that works with no Internet at all, so it is the one left when the
// relay is unreachable, which is exactly when somebody is likely to be
// interfering with the network.

// spoof sends one announcement claiming to be deviceID, from this
// host, pointing wherever the caller says.
func spoof(t *testing.T, group *net.UDPAddr, deviceID string, port int) {
	t.Helper()
	payload, err := json.Marshal(localAnnouncement{
		Magic:    localAnnounceMagic,
		DeviceID: deviceID,
		Port:     port,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	conn, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available: %v", err)
	}
	defer conn.Close()
	_, _ = conn.Write(payload)
}

// garbage sends datagrams that are not announcements at all.
func garbage(t *testing.T, group *net.UDPAddr) {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		return
	}
	defer conn.Close()
	for _, b := range [][]byte{
		{},
		[]byte("not json at all"),
		[]byte(`{"magic":`),
		[]byte(`{"magic":"synctang-local-discovery-v1"}`),
		[]byte(`{"magic":"wrong","device_id":"X","port":1}`),
		make([]byte, 600), // larger than the read buffer
	} {
		_, _ = conn.Write(b)
	}
}

// A key holder must still find the real machine while somebody is
// announcing rubbish, and while somebody is announcing the machine's
// own Device ID pointing at a port with nothing behind it.
func TestLocalDiscoverySurvivesHostileAnnouncements(t *testing.T) {
	multicastOrSkip(t)

	const testPort = defaultLocalPort + 7
	group := &net.UDPAddr{IP: net.ParseIP(defaultLocalGroupAddr), Port: testPort}

	machineCert := testCert(t)
	machineID := deviceIDString(t, machineCert)
	keyholderCert := testCert(t)
	keyholderID := deviceIDString(t, keyholderCert)

	machine := &LocalDiscovery{Cert: machineCert, Port: testPort, AnnounceInterval: 50 * time.Millisecond}
	keyholder := &LocalDiscovery{Cert: keyholderCert, Port: testPort}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The attacker gets there first and keeps going: rubbish, plus the
	// machine's own Device ID pointing at a port with nothing behind
	// it. The genuine machine only starts announcing afterwards, so
	// the dialler is guaranteed to have to get past the bad ones.
	var wg sync.WaitGroup
	noise, stopNoise := context.WithCancel(ctx)
	defer func() { stopNoise(); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for noise.Err() == nil {
			garbage(t, group)
			spoof(t, group, machineID, 1)     // nothing listens on port 1
			spoof(t, group, machineID, 0)     // not a port at all
			spoof(t, group, machineID, 70000) // nor is this
			// Deliberately slower than the machine's own announce
			// interval. The property under test is that bad
			// announcements do not stop discovery working, not that it
			// wins a shouting match, and pacing the attacker keeps the
			// test from turning into a race nobody can rely on.
			time.Sleep(200 * time.Millisecond)
		}
	}()

	dialed := make(chan error, 1)
	go func() {
		conn, err := keyholder.Dial(ctx, DialOptions{PeerID: machineID})
		if err == nil {
			conn.Close()
		}
		dialed <- err
	}()

	// Let the dialler chew on the attacker's announcements for a while
	// before the real machine turns up.
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-dialed:
		t.Fatalf("the dialler gave up on hostile announcements before the machine even appeared: %v", err)
	default:
	}

	listening := make(chan error, 1)
	go func() {
		conn, err := machine.Listen(ctx, ListenOptions{AuthorizedPeers: []string{keyholderID}})
		if err == nil {
			conn.Close()
		}
		listening <- err
	}()

	if err := <-dialed; err != nil {
		t.Fatalf("a key holder must still find the machine through the noise, got %v", err)
	}
	if err := <-listening; err != nil {
		t.Fatalf("machine Listen: %v", err)
	}
	stopNoise()
}

// Ports that cannot exist must be discarded when the announcement is
// read, not turned into a dial that is certain to fail.
func TestLocalDiscoveryIgnoresImpossiblePorts(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 1 << 30} {
		payload, err := json.Marshal(localAnnouncement{
			Magic:    localAnnounceMagic,
			DeviceID: "SOMEBODY",
			Port:     port,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var ann localAnnouncement
		if err := json.Unmarshal(payload, &ann); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if usableAnnouncementPort(ann.Port) {
			t.Errorf("port %d accepted as usable", port)
		}
	}
	for _, port := range []int{1, 22000, 65535} {
		if !usableAnnouncementPort(port) {
			t.Errorf("port %d rejected but is perfectly ordinary", port)
		}
	}
	_ = strconv.Itoa
}

// The claim that makes unauthenticated announcements acceptable is
// that they are only a hint: whoever answers still has to prove, in
// the TLS handshake, that it holds the key behind the Device ID being
// looked for. So an impostor that answers on the announced address,
// with a perfectly valid certificate of its own, must be refused and
// must not stop the genuine machine being found afterwards.
func TestLocalDiscoveryRefusesAnImpostorAndKeepsLooking(t *testing.T) {
	multicastOrSkip(t)

	const testPort = defaultLocalPort + 9
	group := &net.UDPAddr{IP: net.ParseIP(defaultLocalGroupAddr), Port: testPort}

	machineCert := testCert(t)
	machineID := deviceIDString(t, machineCert)
	keyholderCert := testCert(t)
	keyholderID := deviceIDString(t, keyholderCert)
	impostorCert := testCert(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// An impostor listening for real, with its own key, on a port it
	// announces under the machine's Device ID.
	// On all interfaces, not just loopback: the dialler takes the
	// address from the announcement's source IP rather than from
	// anything in the payload, so an attacker can only ever point it
	// at a port on their own machine. That is worth having, and it
	// means this impostor has to be reachable at the sending host's
	// real address.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("impostor listen: %v", err)
	}
	defer ln.Close()
	impostorPort := ln.Addr().(*net.TCPAddr).Port

	impostorTried := make(chan struct{}, 4)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case impostorTried <- struct{}{}:
			default:
			}
			// Answer with a genuine handshake using the wrong identity.
			go func() {
				conn, err := doServerHandshake(impostorCert, slog.Default(), raw, nil, nil)
				if err == nil {
					conn.Close()
				}
			}()
		}
	}()

	var wg sync.WaitGroup
	noise, stopNoise := context.WithCancel(ctx)
	defer func() { stopNoise(); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for noise.Err() == nil {
			spoof(t, group, machineID, impostorPort)
			time.Sleep(200 * time.Millisecond)
		}
	}()

	dialed := make(chan struct {
		id  string
		err error
	}, 1)
	go func() {
		conn, err := (&LocalDiscovery{Cert: keyholderCert, Port: testPort}).
			Dial(ctx, DialOptions{PeerID: machineID})
		var id string
		if err == nil {
			id = conn.PeerID()
			conn.Close()
		}
		dialed <- struct {
			id  string
			err error
		}{id, err}
	}()

	select {
	case <-impostorTried:
	case <-time.After(20 * time.Second):
		t.Fatal("the dialler never tried the announced address at all")
	}

	machine := &LocalDiscovery{Cert: machineCert, Port: testPort, AnnounceInterval: 50 * time.Millisecond}
	listening := make(chan error, 1)
	go func() {
		conn, err := machine.Listen(ctx, ListenOptions{AuthorizedPeers: []string{keyholderID}})
		if err == nil {
			conn.Close()
		}
		listening <- err
	}()

	got := <-dialed
	if got.err != nil {
		t.Fatalf("the genuine machine should still have been found: %v", got.err)
	}
	if got.id != machineID {
		t.Fatalf("connected to %s, which is not the machine that was asked for", got.id)
	}
	if err := <-listening; err != nil {
		t.Fatalf("machine Listen: %v", err)
	}
	stopNoise()
}
