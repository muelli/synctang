// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// Does a machine that is listening keep being reachable when its relay goes away?
//
// The machine is built as the agent builds its transport: a Multi of a
// LocalDiscovery and a SyncthingRelay, listened on again each time a Listen ends, as
// the agent's loop does. The key holder dials through discovery only. Each scenario
// kills relays that are really running (strelaysrv) and measures how long it takes
// until a key holder can dial the machine again, and until discovery names the relay
// the machine is on now. Opt-in because it takes minutes: SYNCTANG_RELAY_RECONNECT=1.
//
//	SYNCTANG_RELAY_RECONNECT=1 STRELAYSRV=/tmp/stbin/strelaysrv STDISCOSRV=/tmp/stbin/stdiscosrv \
//	    go test -count=1 -v -run RelayReconnect ./integration

// relayProc is a strelaysrv that can be killed and started again on the same address
// with the same identity, so that a relay "comes back" as itself.
type relayProc struct {
	t    *testing.T
	keys string
	addr string
	cmd  *exec.Cmd
	out  *syncBuffer
	uri  string
}

func newRelayProc(t *testing.T) *relayProc {
	t.Helper()
	r := &relayProc{
		t:    t,
		keys: t.TempDir(),
		addr: net.JoinHostPort(hostIP().String(), strconv.Itoa(freePort(t))),
	}
	r.start()
	t.Cleanup(r.stop)
	return r
}

func (r *relayProc) start() {
	r.t.Helper()
	r.out = &syncBuffer{}
	// -debug makes the relay log who joins it, which is how a test finds out which relay
	// the machine is on.
	r.cmd = exec.Command(os.Getenv("STRELAYSRV"), "-keys", r.keys, "-listen", r.addr, "-pools=", "-status-srv=", "-debug")
	r.cmd.Stdout, r.cmd.Stderr = r.out, r.out
	if err := r.cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	waitFor(r.t, "the relay to print its URI", func() bool {
		m := relayURIRe.FindStringSubmatch(r.out.String())
		if m == nil {
			return false
		}
		r.uri = m[1]
		return true
	})
}

func (r *relayProc) stop() {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
		r.cmd = nil
	}
}

// host is what discovery shows of this relay: its address.
func (r *relayProc) host() string { return r.addr }

// lastJoin is where in this relay's log the machine's last join request is, or -1.
func (r *relayProc) lastJoin(machineID string) int {
	return strings.LastIndex(r.out.String(), "JoinRelayRequest from "+machineID)
}

// machineUnderTest listens as the agent does, until the test ends.
type machineUnderTest struct {
	id        string
	holderCfg transport.NetworkConfig
	discovery discoveryServer
}

func startAgentLikeMachine(t *testing.T, cfg transport.NetworkConfig, discovery discoveryServer) *machineUnderTest {
	t.Helper()
	cert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	relay := cfg.NewSyncthingRelay(cert)
	relay.AnnounceInterval = 200 * time.Millisecond
	multi := &transport.Multi{
		RetryInterval: 2 * time.Second,
		Transports:    []transport.Transport{&transport.LocalDiscovery{Cert: cert}, relay},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			conn, err := multi.Listen(ctx, transport.ListenOptions{})
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			conn.Close()
		}
	}()
	return &machineUnderTest{
		id:        protocol.NewDeviceID(cert.Certificate[0]).String(),
		holderCfg: holderConfig(t, discovery.pinned()),
		discovery: discovery,
	}
}

// announced is the addresses discovery holds for the machine now.
func (m *machineUnderTest) announced() []string {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a test against its own server.
	resp, err := client.Get(m.discovery.url + "?device=" + m.id)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Addresses []string `json:"addresses"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	return body.Addresses
}

func (m *machineUnderTest) announcesRelayAt(host string) bool {
	for _, a := range m.announced() {
		if strings.Contains(a, host) {
			return true
		}
	}
	return false
}

// canBeDialled tries one dial through discovery and the relay, for at most limit.
func (m *machineUnderTest) canBeDialled(t *testing.T, limit time.Duration) bool {
	t.Helper()
	cert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	conn, err := m.holderCfg.NewSyncthingRelay(cert).Dial(ctx, transport.DialOptions{PeerID: m.id})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// untilDialled polls dials until one works, and says how long it took from since.
func (m *machineUnderTest) untilDialled(t *testing.T, since time.Time, giveUp time.Duration) (time.Duration, bool) {
	t.Helper()
	deadline := time.Now().Add(giveUp)
	for time.Now().Before(deadline) {
		if m.canBeDialled(t, 8*time.Second) {
			return time.Since(since), true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, false
}

func skipUnlessReconnect(t *testing.T) {
	t.Helper()
	skipWithoutServers(t)
	if os.Getenv("SYNCTANG_RELAY_RECONNECT") == "" {
		t.Skip("set SYNCTANG_RELAY_RECONNECT=1 to measure reconnection through a killed relay (takes minutes)")
	}
}

// poolOf serves a relay pool listing the given relays, which can be changed.
func poolOf(t *testing.T, relays ...*relayProc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]string
		for _, rl := range relays {
			list = append(list, map[string]string{"url": rl.uri})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"relays": list})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/endpoint"
}

// Scenario 1: the relay the machine joined is killed while a second relay is in the pool.
func TestRelayReconnectWithAPoolOfTwoWhenTheJoinedOneIsKilled(t *testing.T) {
	skipUnlessReconnect(t)
	discovery := startDiscovery(t)
	a, b := newRelayProc(t), newRelayProc(t)
	machine := startAgentLikeMachine(t, machineConfig(t, poolOf(t, a, b), discovery.pinned()), discovery)

	// No probing dial first: a dial uses up the machine's listen and it joins again, so
	// the relay it is on would not be the one it was found on. The relays' own logs say
	// where it is.
	startedAt := time.Now()
	waitFor(t, "the machine to join a relay", func() bool { return a.lastJoin(machine.id) >= 0 || b.lastJoin(machine.id) >= 0 })
	waitFor(t, "the machine to be announced", func() bool { return len(machine.announced()) > 0 })
	t.Logf("joined and announced after %s", time.Since(startedAt).Round(100*time.Millisecond))
	joined, other := a, b
	if b.lastJoin(machine.id) >= 0 && a.lastJoin(machine.id) < 0 {
		joined, other = b, a
	}
	t.Logf("machine is on %s; the other is %s; discovery holds %v", joined.host(), other.host(), machine.announced())
	if machine.announcesRelayAt(other.host()) {
		t.Log("note: discovery already names the other relay too (an earlier attempt)")
	}
	killedAt := time.Now()
	joined.stop()

	dialled, ok := machine.untilDialled(t, killedAt, 3*time.Minute)
	if !ok {
		t.Fatalf("not reachable 3 minutes after its relay was killed; announced %v", machine.announced())
	}
	t.Logf("reachable again %s after the relay was killed", dialled.Round(100*time.Millisecond))
	waitFor(t, "discovery to name the surviving relay", func() bool { return machine.announcesRelayAt(other.host()) })
	t.Logf("discovery names the surviving relay %s after the kill; record: %v", time.Since(killedAt).Round(100*time.Millisecond), machine.announced())
}

// Scenario 2: every relay dies; one comes back later.
func TestRelayReconnectWhenEveryRelayDiesAndOneComesBack(t *testing.T) {
	skipUnlessReconnect(t)
	discovery := startDiscovery(t)
	a, b := newRelayProc(t), newRelayProc(t)
	machine := startAgentLikeMachine(t, machineConfig(t, poolOf(t, a, b), discovery.pinned()), discovery)
	if _, ok := machine.untilDialled(t, time.Now(), 90*time.Second); !ok {
		t.Fatal("the machine was never reachable to begin with")
	}

	a.stop()
	b.stop()
	const outage = 25 * time.Second
	time.Sleep(outage)
	if machine.canBeDialled(t, 5*time.Second) {
		t.Fatal("the machine was reachable with no relay running")
	}
	restartedAt := time.Now()
	b.start()
	dialled, ok := machine.untilDialled(t, restartedAt, 4*time.Minute)
	if !ok {
		t.Fatalf("not reachable 4 minutes after a relay came back; announced %v", machine.announced())
	}
	t.Logf("after %s with no relay at all, reachable again %s after one came back", outage, dialled.Round(100*time.Millisecond))
}

// Scenario 3: one relay:// configured, and that relay restarts.
func TestRelayReconnectWithASingleConfiguredRelayThatRestarts(t *testing.T) {
	skipUnlessReconnect(t)
	discovery := startDiscovery(t)
	a := newRelayProc(t)
	machine := startAgentLikeMachine(t, machineConfig(t, a.uri, discovery.pinned()), discovery)
	if _, ok := machine.untilDialled(t, time.Now(), 90*time.Second); !ok {
		t.Fatal("the machine was never reachable to begin with")
	}

	a.stop()
	const outage = 10 * time.Second
	time.Sleep(outage)
	restartedAt := time.Now()
	a.start()
	dialled, ok := machine.untilDialled(t, restartedAt, 3*time.Minute)
	if !ok {
		t.Fatalf("not reachable 3 minutes after its relay restarted; announced %v", machine.announced())
	}
	t.Logf("after %s down, reachable again %s after the relay restarted", outage, dialled.Round(100*time.Millisecond))
}

// Scenario 4: the relay stops answering without closing anything, as a firewall that
// forgets a connection or a relay that hangs would. Nothing tells the machine; the
// time to notice is the relay client's own message timeout, and then a second relay in
// the pool takes over.
func TestRelayReconnectWhenTheJoinedRelayGoesSilent(t *testing.T) {
	skipUnlessReconnect(t)
	discovery := startDiscovery(t)
	a, b := newRelayProc(t), newRelayProc(t)
	machine := startAgentLikeMachine(t, machineConfig(t, poolOf(t, a, b), discovery.pinned()), discovery)
	waitFor(t, "the machine to join a relay", func() bool { return a.lastJoin(machine.id) >= 0 || b.lastJoin(machine.id) >= 0 })
	waitFor(t, "the machine to be announced", func() bool { return len(machine.announced()) > 0 })
	joined, other := a, b
	if b.lastJoin(machine.id) >= 0 && a.lastJoin(machine.id) < 0 {
		joined, other = b, a
	}
	t.Logf("machine is on %s; freezing it (SIGSTOP), the other is %s", joined.host(), other.host())
	frozenAt := time.Now()
	if err := joined.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = joined.cmd.Process.Signal(syscall.SIGCONT) })

	waitFor2 := func() bool { return machine.announcesRelayAt(other.host()) }
	deadline := time.Now().Add(5 * time.Minute)
	for !waitFor2() {
		if time.Now().After(deadline) {
			t.Fatalf("the machine did not move to the other relay in 5 minutes; record %v", machine.announced())
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("discovery names the other relay %s after the first one went silent", time.Since(frozenAt).Round(100*time.Millisecond))
	if d, ok := machine.untilDialled(t, frozenAt, time.Minute); ok {
		t.Logf("dialled %s after it went silent", d.Round(100*time.Millisecond))
	} else {
		t.Error("the machine moved but could not be dialled")
	}
}
