// SPDX-License-Identifier: AGPL-3.0-or-later

package transport_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// Two identities talk over a direct loopback TCP connection. With
// DirectAddr unset, the same code finds its peer through Syncthing
// discovery and the relay network instead.
func Example() {
	dir, err := os.MkdirTemp("", "transport-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)

	// Each side keeps one long-lived certificate; its Device ID is the
	// identity the other side pins.
	listenerCert, err := transport.LoadOrCreateCert(filepath.Join(dir, "listener.pem"))
	if err != nil {
		fmt.Println(err)
		return
	}
	dialerCert, err := transport.LoadOrCreateCert(filepath.Join(dir, "dialer.pem"))
	if err != nil {
		fmt.Println(err)
		return
	}
	listener := transport.NewSyncthingRelay(listenerCert)
	dialer := transport.NewSyncthingRelay(dialerCert)

	// Reserve a loopback port for the example.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println(err)
		return
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A Device ID is the SHA-256 of the certificate's DER bytes in
	// Syncthing's encoding. In a real deployment each side learns the
	// other's ID out of band when pairing.
	listenerID := protocol.NewDeviceID(listenerCert.Certificate[0]).String()
	dialerID := protocol.NewDeviceID(dialerCert.Certificate[0]).String()

	type result struct {
		conn transport.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := listener.Listen(ctx, transport.ListenOptions{
			DirectAddr:      addr,
			AuthorizedPeers: []string{dialerID},
		})
		accepted <- result{conn, err}
	}()

	conn, err := dialer.Dial(ctx, transport.DialOptions{PeerID: listenerID, DirectAddr: addr})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	res := <-accepted
	if res.err != nil {
		fmt.Println(res.err)
		return
	}
	defer res.conn.Close()

	go res.conn.Write([]byte("pong"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(buf), res.conn.PeerID() == dialerID, conn.PeerID() == listenerID)
	// Output: pong true true
}
