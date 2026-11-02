// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/muelli/synctang/mrcore/transport"
)

func TestNewDialTransportAppliesNetworkConfig(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := transport.ParseNetworkConfig("", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, direct := range map[string]string{"relay and local discovery": "", "direct address": "127.0.0.1:1"} {
		t.Run(name, func(t *testing.T) {
			var r *transport.SyncthingRelay
			switch v := newDialTransport(cert, direct, cfg).(type) {
			case *transport.SyncthingRelay:
				r = v
			case *transport.Multi:
				for _, leg := range v.Transports {
					if x, ok := leg.(*transport.SyncthingRelay); ok {
						r = x
					}
				}
			}
			if r == nil {
				t.Fatal("no SyncthingRelay leg")
			}
			if r.DiscoveryURL != "https://disco.example.net/v2/" {
				t.Errorf("DiscoveryURL = %q", r.DiscoveryURL)
			}
		})
	}
}
