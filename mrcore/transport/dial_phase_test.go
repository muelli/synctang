// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// phaseLog records the dial phases a transport reports, in order.
type phaseLog struct {
	mu     sync.Mutex
	events []string
}

func (p *phaseLog) record(route Route, phase DialPhase) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, string(route)+":"+phase.String())
}

func (p *phaseLog) get() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// A machine reached at a plain address: connecting, then checking who it is.
func TestDialReportsConnectThenHandshakeForADirectAddress(t *testing.T) {
	machine := &SyncthingRelay{Cert: testCert(t)}
	addr := freeLoopbackAddr(t)
	go func() {
		conn, err := machine.Listen(context.Background(), ListenOptions{DirectAddr: addr})
		if err == nil {
			conn.Close()
		}
	}()

	var log phaseLog
	key := &SyncthingRelay{Cert: testCert(t), OnDialPhase: log.record}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	machineID := protocol.NewDeviceID(machine.Cert.Certificate[0]).String()
	conn, err := key.Dial(ctx, DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	if got, want := log.get(), []string{"direct:connect", "direct:handshake"}; !equalStrings(got, want) {
		t.Errorf("phases %v, want %v", got, want)
	}
}

// Through discovery: looking the machine up, then reaching the address it gave,
// then the handshake, in that order.
func TestDialReportsLookupConnectHandshakeThroughDiscovery(t *testing.T) {
	machine := &SyncthingRelay{Cert: testCert(t)}
	machineID := protocol.NewDeviceID(machine.Cert.Certificate[0]).String()
	addr := freeLoopbackAddr(t)
	go func() {
		conn, err := machine.Listen(context.Background(), ListenOptions{DirectAddr: addr})
		if err == nil {
			conn.Close()
		}
	}()
	disco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"addresses": []string{"tcp://" + addr}})
	}))
	defer disco.Close()

	var log phaseLog
	key := &SyncthingRelay{Cert: testCert(t), DiscoveryURL: disco.URL + "/v2/?insecure", OnDialPhase: log.record}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := key.Dial(ctx, DialOptions{PeerID: machineID})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	got := log.get()
	want := []string{"relay:lookup", "direct:connect", "direct:handshake"}
	if !equalStrings(got, want) {
		t.Errorf("phases %v, want %v", got, want)
	}
}

// A lookup that finds nothing leaves the last phase reached as the lookup:
// nothing later is reported for a step that never happened.
func TestDialThatFindsNothingReportsOnlyTheLookup(t *testing.T) {
	disco := httptest.NewServer(http.NotFoundHandler())
	defer disco.Close()
	var log phaseLog
	key := &SyncthingRelay{Cert: testCert(t), DiscoveryURL: disco.URL + "/v2/?insecure", OnDialPhase: log.record}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := key.Dial(ctx, DialOptions{PeerID: someDeviceID}); err == nil {
		t.Fatal("a dial to nobody succeeded")
	}
	for _, e := range log.get() {
		if e != "relay:lookup" {
			t.Errorf("unexpected phase %q after a lookup that found nothing", e)
		}
	}
	if len(log.get()) == 0 {
		t.Error("no phase was reported")
	}
}

func TestLocalDiscoveryDialReportsLooking(t *testing.T) {
	var log phaseLog
	l := &LocalDiscovery{Cert: testCert(t), OnDialPhase: log.record}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = l.Dial(ctx, DialOptions{PeerID: someDeviceID})
	got := log.get()
	if len(got) == 0 || got[0] != "local:lookup" {
		t.Errorf("phases %v, want the first to be local:lookup", got)
	}
}

// Multi says which of its transports won, so the app can say how it got there.
func TestMultiReportsTheWinningTransport(t *testing.T) {
	winner := -1
	m := &Multi{
		Transports: []Transport{
			&fakeTransport{delay: 2 * time.Second, conn: &fakeConn{id: "slow"}},
			&fakeTransport{delay: 10 * time.Millisecond, conn: &fakeConn{id: "fast"}},
		},
		OnWin: func(i int) { winner = i },
	}
	conn, err := m.Dial(context.Background(), DialOptions{PeerID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if winner != 1 {
		t.Errorf("winner = %d, want 1", winner)
	}
}
