// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
	socket "syncthing-socket"
)

// freeAddr reserves an ephemeral TCP port on the loopback interface and
// hands back its address as a string, closing the listener straight away.
// The window between closing and the real listener rebinding it is a
// theoretical race, not one this test suite is expected to hit.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing probe listener: %v", err)
	}
	return addr
}

// TestDirectListenDialRoundTrip is T2.1: one Listen and one Dial over a
// direct TCP address, both sides complete the handshake, each side's
// PeerID matches the other side's certificate-derived Device ID
// independently, and a request/response pair round-trips.
func TestDirectListenDialRoundTrip(t *testing.T) {
	t.Parallel()

	listenerCert, err := socket.GenerateDeterministicCert("t2.1-pairing-secret-listener")
	if err != nil {
		t.Fatalf("generating listener cert: %v", err)
	}
	dialerCert, err := socket.GenerateDeterministicCert("t2.1-pairing-secret-dialer")
	if err != nil {
		t.Fatalf("generating dialer cert: %v", err)
	}

	wantListenerID := protocol.NewDeviceID(listenerCert.Certificate[0]).String()
	wantDialerID := protocol.NewDeviceID(dialerCert.Certificate[0]).String()

	addr := freeAddr(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listener := NewSyncthingRelay(listenerCert)
	dialer := NewSyncthingRelay(dialerCert)

	type listenResult struct {
		conn Conn
		err  error
	}
	listenCh := make(chan listenResult, 1)
	go func() {
		conn, err := listener.Listen(ctx, ListenOptions{DirectAddr: addr})
		listenCh <- listenResult{conn, err}
	}()

	dialerConn, err := dialer.Dial(ctx, DialOptions{PeerID: wantListenerID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer dialerConn.Close()

	res := <-listenCh
	if res.err != nil {
		t.Fatalf("Listen: %v", res.err)
	}
	listenerConn := res.conn
	defer listenerConn.Close()

	if listenerConn.PeerID() != wantDialerID {
		t.Errorf("listener side PeerID = %q, want %q (dialer's Device ID)", listenerConn.PeerID(), wantDialerID)
	}
	if dialerConn.PeerID() != wantListenerID {
		t.Errorf("dialer side PeerID = %q, want %q (listener's Device ID)", dialerConn.PeerID(), wantListenerID)
	}

	const request = "kid+X request"
	if _, err := dialerConn.Write([]byte(request)); err != nil {
		t.Fatalf("dialer write: %v", err)
	}
	buf := make([]byte, len(request))
	if _, err := io.ReadFull(listenerConn, buf); err != nil {
		t.Fatalf("listener read: %v", err)
	}
	if string(buf) != request {
		t.Errorf("listener received %q, want %q", buf, request)
	}

	const response = "Y-or-xOnly response"
	if _, err := listenerConn.Write([]byte(response)); err != nil {
		t.Fatalf("listener write: %v", err)
	}
	buf2 := make([]byte, len(response))
	if _, err := io.ReadFull(dialerConn, buf2); err != nil {
		t.Fatalf("dialer read: %v", err)
	}
	if string(buf2) != response {
		t.Errorf("dialer received %q, want %q", buf2, response)
	}
}

// TestDirectListenRejectsUnauthorizedPeer is T2.2: Listen with
// AuthorizedPeers naming one specific Device ID must reject a dialer
// with a different identity, after the TLS handshake itself has
// succeeded, and leave the connection unusable on both sides afterwards.
func TestDirectListenRejectsUnauthorizedPeer(t *testing.T) {
	t.Parallel()

	listenerCert, err := socket.GenerateDeterministicCert("t2.2-pairing-secret-listener")
	if err != nil {
		t.Fatalf("generating listener cert: %v", err)
	}
	authorizedCert, err := socket.GenerateDeterministicCert("t2.2-pairing-secret-authorized")
	if err != nil {
		t.Fatalf("generating authorized-peer cert: %v", err)
	}
	unauthorizedCert, err := socket.GenerateDeterministicCert("t2.2-pairing-secret-intruder")
	if err != nil {
		t.Fatalf("generating unauthorized-peer cert: %v", err)
	}

	wantListenerID := protocol.NewDeviceID(listenerCert.Certificate[0]).String()
	authorizedID := protocol.NewDeviceID(authorizedCert.Certificate[0]).String()
	unauthorizedID := protocol.NewDeviceID(unauthorizedCert.Certificate[0]).String()

	addr := freeAddr(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listener := NewSyncthingRelay(listenerCert)
	intruder := NewSyncthingRelay(unauthorizedCert)

	type listenResult struct {
		conn Conn
		err  error
	}
	listenCh := make(chan listenResult, 1)
	go func() {
		conn, err := listener.Listen(ctx, ListenOptions{
			DirectAddr:      addr,
			AuthorizedPeers: []string{authorizedID},
		})
		listenCh <- listenResult{conn, err}
	}()

	dialerConn, err := intruder.Dial(ctx, DialOptions{PeerID: wantListenerID, DirectAddr: addr})
	// The TLS handshake and Device ID verification between dialer and
	// listener succeed regardless of the authorization list (that list
	// is enforced only on the listener side); Dial itself should
	// therefore not fail here.
	if err != nil {
		t.Fatalf("Dial (handshake against listener should succeed even though listener will later reject): %v", err)
	}
	defer dialerConn.Close()

	res := <-listenCh
	if res.err == nil {
		if res.conn != nil {
			res.conn.Close()
		}
		t.Fatalf("Listen: expected rejection of unauthorized peer %s, got success", unauthorizedID)
	}
	if !strings.Contains(res.err.Error(), unauthorizedID) {
		t.Errorf("Listen error %q does not name the rejected peer's Device ID %q; the listener must have verified the peer's cryptographic identity before rejecting it", res.err, unauthorizedID)
	}

	// The connection must be unusable afterwards: the listener closed
	// its side once it rejected the peer, so a subsequent read on the
	// dialer's side must error rather than hang or succeed.
	readErrCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := dialerConn.Read(buf)
		readErrCh <- err
	}()
	select {
	case err := <-readErrCh:
		if err == nil {
			t.Errorf("dialer read after rejection: expected an error, got none")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("dialer read after rejection: timed out, connection was not closed")
	}
}

// TestListenContextCancellation checks that Listen returns promptly, with
// an error wrapping ctx's error, when no dialer ever shows up and the
// caller's context is done. This is not one of the two required tests
// but guards against Listen blocking forever on a dead pairing attempt.
func TestListenContextCancellation(t *testing.T) {
	t.Parallel()

	cert, err := socket.GenerateDeterministicCert("cancellation-test-listener")
	if err != nil {
		t.Fatalf("generating cert: %v", err)
	}

	addr := freeAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	listener := NewSyncthingRelay(cert)

	start := time.Now()
	_, err = listener.Listen(ctx, ListenOptions{DirectAddr: addr})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("Listen: expected a context-deadline error, got none")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Listen error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Listen took %v to notice context cancellation, want well under 5s", elapsed)
	}
}
