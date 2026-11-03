// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// What happens to a session through a relay when nobody says anything.
//
// An unlock leaves the phone waiting for a person to approve, and the phone
// cannot send anything meanwhile without changing the protocol. Sessions have been
// seen to end with EOF some 90 to 100 seconds after connecting, well before the
// machine's own three-minute cap, so this measures when and how an idle session
// closes, on a private relay with the real strelaysrv, and with which of its
// flags the time changes. It is a measurement, not a pass/fail test of a
// property: it logs what it saw, and fails only if the session was still open
// when it gave up looking, or if a side saw something other than a close.
//
// Opt-in because it takes minutes. SYNCTANG_RELAY_IDLE=short runs the variant
// with the relay's network timeout lowered to 20 s; =long also runs the relay's
// defaults (network timeout 2 min, ping interval 1 min), which takes about three
// minutes; =all adds a run with only the ping interval lowered.
//
//	SYNCTANG_RELAY_IDLE=long STRELAYSRV=/tmp/stbin/strelaysrv STDISCOSRV=/tmp/stbin/stdiscosrv \
//	    go test -count=1 -v -run RelayIdle ./integration
func TestRelayIdleSessionClose(t *testing.T) {
	mode := os.Getenv("SYNCTANG_RELAY_IDLE")
	if mode == "" {
		t.Skip("set SYNCTANG_RELAY_IDLE=short, long or all to measure how an idle relay session ends")
	}
	skipWithoutServers(t)

	type variant struct {
		name   string
		args   []string
		runIn  []string
		giveUp time.Duration
	}
	variants := []variant{
		{"network-timeout 20s", []string{"-network-timeout", "20s"}, []string{"short", "long", "all"}, 90 * time.Second},
		{"defaults (network-timeout 2m, ping-interval 1m)", nil, []string{"long", "all"}, 5 * time.Minute},
		{"ping-interval 10s only", []string{"-ping-interval", "10s"}, []string{"all"}, 5 * time.Minute},
	}
	for _, v := range variants {
		selected := false
		for _, m := range v.runIn {
			if m == mode {
				selected = true
			}
		}
		if !selected {
			continue
		}
		t.Run(v.name, func(t *testing.T) { measureIdle(t, v.args, v.giveUp) })
	}
}

func measureIdle(t *testing.T, relayArgs []string, giveUp time.Duration) {
	discovery := startDiscovery(t)
	relay := startRelay(t, relayArgs...)
	machineCfg := machineConfig(t, relay, discovery.pinned())
	holderCfg := holderConfig(t, discovery.pinned())

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()
	holderID := protocol.NewDeviceID(holderCert.Certificate[0]).String()

	ctx, cancel := context.WithTimeout(context.Background(), giveUp+2*time.Minute)
	defer cancel()
	machineTr := machineCfg.NewSyncthingRelay(machineCert)
	machineTr.AnnounceInterval = 200 * time.Millisecond

	type ended struct {
		at  time.Time
		err error
	}
	machineEnded := make(chan ended, 1)
	var connectedAt time.Time
	machineReady := make(chan struct{})
	go func() {
		conn, err := listenAgain(ctx, machineTr, transport.ListenOptions{AuthorizedPeers: []string{holderID}})
		if err != nil {
			machineEnded <- ended{time.Now(), err}
			return
		}
		defer conn.Close()
		close(machineReady)
		// Say hello once, as a machine does, then say nothing more.
		_, _ = conn.Write([]byte("hello"))
		buf := make([]byte, 64)
		for {
			if _, err := conn.Read(buf); err != nil {
				machineEnded <- ended{time.Now(), err}
				return
			}
		}
	}()

	conn, err := holderCfg.NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	connectedAt = time.Now()
	<-machineReady
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("the hello did not arrive: %v", err)
	}
	lastTraffic := time.Now()
	t.Logf("connected; last traffic at +%s after the dial returned", lastTraffic.Sub(connectedAt).Round(time.Millisecond))

	holderEnded := make(chan ended, 1)
	go func() {
		b := make([]byte, 64)
		for {
			if _, err := conn.Read(b); err != nil {
				holderEnded <- ended{time.Now(), err}
				return
			}
		}
	}()

	var h, m ended
	timeout := time.After(giveUp)
	for got := 0; got < 2; {
		select {
		case h = <-holderEnded:
			t.Logf("key holder's read ended %s after the last traffic: %v", h.at.Sub(lastTraffic).Round(100*time.Millisecond), h.err)
			got++
			holderEnded = nil
		case m = <-machineEnded:
			t.Logf("machine's read ended %s after the last traffic: %v", m.at.Sub(lastTraffic).Round(100*time.Millisecond), m.err)
			got++
			machineEnded = nil
		case <-timeout:
			t.Fatalf("the idle session was still open %s after the last traffic", giveUp)
		}
	}
}
