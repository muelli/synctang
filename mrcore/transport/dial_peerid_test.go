// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net"
	"testing"
	"time"
)

// A dial with no PeerID pins nothing: the handshake would succeed with
// whoever answers. Every transport must refuse it, rather than leave it to
// each caller to remember to pass one.
func TestDialRefusesAnEmptyPeerID(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	transports := map[string]Transport{
		"SyncthingRelay": NewSyncthingRelay(testCert(t)),
		"LocalDiscovery": &LocalDiscovery{Cert: testCert(t)},
		"Multi":          &Multi{Transports: []Transport{NewSyncthingRelay(testCert(t))}},
	}
	for name, tr := range transports {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, err := tr.Dial(ctx, DialOptions{DirectAddr: ln.Addr().String()})
			if err == nil {
				conn.Close()
				t.Fatal("Dial with an empty PeerID succeeded")
			}
			if ctx.Err() != nil {
				t.Fatalf("Dial did not refuse an empty PeerID up front: %v", err)
			}
		})
	}
}
