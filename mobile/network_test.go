// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/muelli/syncthing-socket/identity"
)

func TestDialTransportAppliesNetworkConfig(t *testing.T) {
	cert, err := identity.GenerateDeterministicCert("test seed for network settings")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := transport.ParseNetworkConfig("", "https://disco.example.net/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	multi, ok := newDialTransport(cert, "", cfg).(*transport.Multi)
	if !ok {
		t.Fatal("want a Multi without a direct address")
	}
	var found bool
	for _, leg := range multi.Transports {
		if r, ok := leg.(*transport.SyncthingRelay); ok {
			found = true
			if r.DiscoveryURL != "https://disco.example.net/v2/" {
				t.Errorf("DiscoveryURL = %q", r.DiscoveryURL)
			}
		}
	}
	if !found {
		t.Error("no SyncthingRelay leg")
	}
}

func TestSetNetworkValidates(t *testing.T) {
	s := NewSession("MACHINE", "seed")
	if err := s.SetNetwork("", "ftp://disco.example.net/"); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("Session.SetNetwork accepted a bad discovery URL: %v", err)
	}
	if err := s.SetNetwork("gopher://pool.example.net/", ""); err == nil || !strings.Contains(err.Error(), "relay-pool") {
		t.Errorf("Session.SetNetwork accepted a bad relay URL: %v", err)
	}
	if err := s.SetNetwork("", "https://disco.example.net/v2/"); err != nil {
		t.Errorf("Session.SetNetwork refused a good URL: %v", err)
	}
	p := NewPairer()
	if err := p.SetNetwork("", "ftp://disco.example.net/"); err == nil {
		t.Error("Pairer.SetNetwork accepted a bad discovery URL")
	}
	if err := p.SetNetwork("", "https://disco.example.net/v2/"); err != nil {
		t.Errorf("Pairer.SetNetwork refused a good URL: %v", err)
	}
	// Empty means the public servers, and must be accepted: it is what a
	// machine paired without any setting stores.
	if err := p.SetNetwork("", ""); err != nil {
		t.Errorf("Pairer.SetNetwork refused the defaults: %v", err)
	}
}

// A phone told about a private discovery server must look the machine
// up there and nowhere else. A fake discovery server answers with the
// address of a machine listening on the loopback; the session reaching
// it proves the lookup went to the server it was given.
func TestSessionLooksTheMachineUpOnTheGivenDiscoveryServer(t *testing.T) {
	const (
		phoneSeed   = "test phone seed for a private discovery server"
		machineSeed = "test machine seed for a private discovery server"
	)
	phoneID, err := DeviceIDForSeed(phoneSeed)
	if err != nil {
		t.Fatal(err)
	}
	machineID, err := DeviceIDForSeed(machineSeed)
	if err != nil {
		t.Fatal(err)
	}
	machineCert, err := identity.GenerateDeterministicCert(machineSeed)
	if err != nil {
		t.Fatal(err)
	}

	// A port that is free now; the transport opens its own listener on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	var lookups atomic.Int32
	disco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("device") != machineID {
			http.NotFound(w, r)
			return
		}
		lookups.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"addresses": []string{"tcp://" + addr}})
	}))
	defer disco.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	machineErr := make(chan error, 1)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{phoneID}, DirectAddr: addr})
		if err != nil {
			machineErr <- err
			return
		}
		defer conn.Close()
		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: "private"}); err != nil {
			machineErr <- err
			return
		}
		g := mrcore.P256()
		scalar, _ := g.RandomScalar()
		point, _ := g.ScalarBaseMult(scalar)
		machineErr <- mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: []byte("k"), X: point})
	}()

	session := NewSession(machineID, phoneSeed)
	if err := session.SetNetwork("", disco.URL+"/v2/"); err != nil {
		t.Fatalf("SetNetwork: %v", err)
	}
	session.SetTimeoutSeconds(25)
	defer session.Close()
	if err := session.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if lookups.Load() == 0 {
		t.Error("the machine was reached without a lookup on the given discovery server")
	}
	if err := <-machineErr; err != nil {
		t.Fatalf("machine side: %v", err)
	}
}
