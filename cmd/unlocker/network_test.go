// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/muelli/synctang/mrcore/transport"
)

// syncthingRelayLeg finds the SyncthingRelay inside what
// newAgentTransport returned, whichever shape it came in.
func syncthingRelayLeg(t *testing.T, tr transport.Transport) *transport.SyncthingRelay {
	t.Helper()
	switch v := tr.(type) {
	case *transport.SyncthingRelay:
		return v
	case *transport.Multi:
		for _, leg := range v.Transports {
			if r, ok := leg.(*transport.SyncthingRelay); ok {
				return r
			}
		}
	}
	t.Fatalf("no SyncthingRelay in %T", tr)
	return nil
}

func TestNewAgentTransportAppliesNetworkConfig(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := transport.ParseNetworkConfig("https://pool.example.net/endpoint", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, direct := range map[string]string{"relay and local discovery": "", "direct address": "127.0.0.1:0"} {
		t.Run(name, func(t *testing.T) {
			r := syncthingRelayLeg(t, newAgentTransport(cert, nil, direct, cfg))
			if r.RelayPoolURL != "dynamic+https://pool.example.net/endpoint" {
				t.Errorf("RelayPoolURL = %q", r.RelayPoolURL)
			}
			if r.DiscoveryURL != "https://disco.example.net/v2/" {
				t.Errorf("DiscoveryURL = %q", r.DiscoveryURL)
			}
			if len(r.AnnounceURLs) != 1 || r.AnnounceURLs[0] != "https://disco.example.net/v2/" {
				t.Errorf("AnnounceURLs = %v", r.AnnounceURLs)
			}
		})
	}
}

func TestNewAgentTransportZeroConfigKeepsDefaults(t *testing.T) {
	cert, err := transport.LoadOrCreateCert(t.TempDir() + "/id.pem")
	if err != nil {
		t.Fatal(err)
	}
	r := syncthingRelayLeg(t, newAgentTransport(cert, nil, "", transport.NetworkConfig{}))
	if r.RelayPoolURL != "" || r.DiscoveryURL != "" || len(r.AnnounceURLs) != 0 {
		t.Errorf("defaults must stay in place: %+v", r)
	}
}
