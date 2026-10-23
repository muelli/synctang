// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func shortHandshakeTimeout(t *testing.T) {
	t.Helper()
	old := handshakeTimeout
	handshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = old })
}

// A peer that connects and then says nothing must not hold a listener
// leg for the rest of the boot: that would let anyone who knows the
// machine's Device ID take it off the air.
func TestServerHandshakeGivesUpOnASilentPeer(t *testing.T) {
	shortHandshakeTimeout(t)
	server, silent := net.Pipe()
	defer silent.Close()

	closed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := doServerHandshake(testCert(t), quietLogger(), server, nil, func() { close(closed) })
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake with a silent peer reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server handshake did not give up on a peer that never speaks")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("onClose was not called after the handshake timed out")
	}
}

// The dialling side has the same problem: a forged LAN announcement can
// point it at a port that accepts and stalls.
func TestClientHandshakeGivesUpOnASilentPeer(t *testing.T) {
	shortHandshakeTimeout(t)
	client, silent := net.Pipe()
	defer silent.Close()

	done := make(chan error, 1)
	go func() {
		_, err := doClientHandshake(testCert(t), quietLogger(), client, "")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake with a silent peer reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client handshake did not give up on a peer that never speaks")
	}
}

// The deadline is for the handshake only. A connection that has
// completed it must stay usable after the timeout would have fired.
func TestHandshakeDeadlineIsClearedOnSuccess(t *testing.T) {
	shortHandshakeTimeout(t)
	serverCert, clientCert := testCert(t), testCert(t)
	a, b := net.Pipe()

	type result struct {
		c   Conn
		err error
	}
	srv := make(chan result, 1)
	go func() {
		c, err := doServerHandshake(serverCert, quietLogger(), a, nil, nil)
		srv <- result{c, err}
	}()
	cli, err := doClientHandshake(clientCert, quietLogger(), b, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	s := <-srv
	if s.err != nil {
		t.Fatal(s.err)
	}
	defer s.c.Close()

	time.Sleep(2 * handshakeTimeout)
	go func() { _, _ = cli.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(s.c, buf); err != nil {
		t.Fatalf("connection died after the handshake deadline: %v", err)
	}
}
