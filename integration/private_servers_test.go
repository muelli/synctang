// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integration holds tests that need programs this module does not
// build itself. They skip when those are not there.
//
// private_servers_test.go runs a machine and a key holder through a relay
// server and a discovery server of their own, with the real Syncthing
// programs strelaysrv and stdiscosrv rather than fakes of them. The
// discovery server makes itself a self-signed certificate, which no system
// root vouches for, so the tests also show what the ?id= and ?insecure
// options of a discovery URL do against a real server: ?id=<its Device ID>
// is accepted, a different id is refused before connecting, ?insecure is
// accepted, and neither is refused. Nothing in them is pointed at the public
// Syncthing servers: the settings under test are exactly what keeps them from
// doing so, and a lookup that went there instead would not find the machine.
// They do not, however, take the network away, so that is argued from the
// settings and not enforced.
//
// The servers are reached on this host's own non-loopback address, not on
// 127.0.0.1: stdiscosrv silently drops loopback addresses from what it is
// told, so a relay announced as relay://127.0.0.1:... is never stored and the
// machine is never found. A private deployment is on a LAN address anyway.
// The tests skip on a host with none.
//
// To run them, build the two servers somewhere outside this module (their
// module's replace directives stop "go install ...@version" from working, and
// their dependencies are not in this module's go.sum):
//
//	mkdir /tmp/stbuild && cd /tmp/stbuild
//	printf 'module stbuild\n\ngo 1.25\n\nrequire github.com/syncthing/syncthing v1.30.0\n' > go.mod
//	GOFLAGS=-mod=mod go build -o /tmp/stbin/ \
//	    github.com/syncthing/syncthing/cmd/strelaysrv github.com/syncthing/syncthing/cmd/stdiscosrv
//
// and then:
//
//	STRELAYSRV=/tmp/stbin/strelaysrv STDISCOSRV=/tmp/stbin/stdiscosrv go test ./integration
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// hostIP is this host's first non-loopback IPv4 address, or nil.
func hostIP() net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP
		}
	}
	return nil
}

// syncBuffer is a bytes.Buffer a process can write while a test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", hostIP().String()+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// start runs a server for the length of the test and returns what it prints.
func start(t *testing.T, bin string, args ...string) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", filepath.Base(bin), err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// discoveryServer is a running stdiscosrv: where it is, and the Device ID of
// the self-signed certificate it made for itself.
type discoveryServer struct {
	url string // https://host:port/v2/, no options
	id  string
}

// with is the server's URL with a query, such as "?id=" + s.id.
func (d discoveryServer) with(query string) string { return d.url + query }

// pinned is the URL as an owner would write it: trusted by Device ID.
func (d discoveryServer) pinned() string { return d.with("?id=" + d.id) }

// startDiscovery runs stdiscosrv, which generates its own certificate when
// there is none, and reads the Device ID off the file it wrote.
func startDiscovery(t *testing.T) discoveryServer {
	t.Helper()
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	port := freePort(t)
	addr := net.JoinHostPort(hostIP().String(), strconv.Itoa(port))
	out := start(t, os.Getenv("STDISCOSRV"),
		"--cert", certFile, "--key", filepath.Join(dir, "key.pem"),
		"--listen", addr, "--db-dir", dir)
	waitFor(t, "the discovery server to listen\n"+out.String(), func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	raw, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("the discovery server wrote no certificate: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("the discovery server's certificate is not PEM")
	}
	return discoveryServer{
		url: fmt.Sprintf("https://%s/v2/", addr),
		id:  protocol.NewDeviceID(block.Bytes).String(),
	}
}

var relayURIRe = regexp.MustCompile(`URI: (relay://\S+)`)

// startRelay runs strelaysrv on the host's address, joined to no pool, and
// returns the relay:// URI it prints, which carries its Device ID.
func startRelay(t *testing.T) string {
	t.Helper()
	port := freePort(t)
	out := start(t, os.Getenv("STRELAYSRV"),
		"-keys", t.TempDir(), "-listen", net.JoinHostPort(hostIP().String(), strconv.Itoa(port)),
		"-pools=", "-status-srv=")
	var uri string
	waitFor(t, "the relay to print its URI\n"+out.String(), func() bool {
		m := relayURIRe.FindStringSubmatch(out.String())
		if m == nil {
			return false
		}
		uri = m[1]
		return true
	})
	return uri
}

func skipWithoutServers(t *testing.T) {
	t.Helper()
	if os.Getenv("STRELAYSRV") == "" || os.Getenv("STDISCOSRV") == "" {
		t.Skip("set STRELAYSRV and STDISCOSRV to the strelaysrv and stdiscosrv binaries (see the top of this file)")
	}
	if hostIP() == nil {
		t.Skip("this host has no non-loopback IPv4 address, and stdiscosrv drops loopback ones")
	}
}

func machineConfig(t *testing.T, relay, discovery string) transport.NetworkConfig {
	t.Helper()
	cfg, err := transport.ParseNetworkConfig(relay, discovery, "")
	if err != nil {
		t.Fatalf("the machine's settings: %v", err)
	}
	return cfg
}

func holderConfig(t *testing.T, discovery string) transport.NetworkConfig {
	t.Helper()
	cfg, err := transport.ParseDialerNetworkConfig(discovery, "")
	if err != nil {
		t.Fatalf("the key holder's settings: %v", err)
	}
	return cfg
}

// listenAgain is what the agent does: a Listen that fails (a session the relay
// could not complete, say, which closes the machine's control connection)
// is followed by another, until one succeeds or ctx is done. A single Listen
// would make these tests depend on the first dial landing cleanly.
func listenAgain(ctx context.Context, tr *transport.SyncthingRelay, opts transport.ListenOptions) (transport.Conn, error) {
	for {
		conn, err := tr.Listen(ctx, opts)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// meet has the machine listen through machineCfg and the key holder dial
// through holderCfg, and checks that bytes cross both ways and that each
// side saw the other's real identity.
func meet(t *testing.T, machineCfg, holderCfg transport.NetworkConfig) {
	t.Helper()
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	machineTr := machineCfg.NewSyncthingRelay(machineCert)
	machineTr.AnnounceInterval = 200 * time.Millisecond
	type result struct {
		peer string
		got  string
		err  error
	}
	listened := make(chan result, 1)
	go func() {
		conn, err := listenAgain(ctx, machineTr, transport.ListenOptions{AuthorizedPeers: []string{holderID}})
		if err != nil {
			listened <- result{err: err}
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("hello from the machine")); err != nil {
			listened <- result{err: err}
			return
		}
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		listened <- result{peer: conn.PeerID(), got: string(buf[:n]), err: err}
	}()

	conn, err := holderCfg.NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err != nil {
		t.Fatalf("the key holder could not reach the machine: %v", err)
	}
	defer conn.Close()
	if conn.PeerID() != machineID {
		t.Errorf("key holder saw peer %s, want %s", conn.PeerID(), machineID)
	}
	buf := make([]byte, 64)
	n, err := io.ReadAtLeast(conn, buf, len("hello from the machine"))
	if err != nil || string(buf[:n]) != "hello from the machine" {
		t.Fatalf("key holder read %q, %v", buf[:n], err)
	}
	if _, err := conn.Write([]byte("hello from the key holder")); err != nil {
		t.Fatal(err)
	}
	r := <-listened
	if r.err != nil {
		t.Fatalf("machine side: %v", r.err)
	}
	if r.peer != holderID || r.got != "hello from the key holder" {
		t.Errorf("machine saw peer %s saying %q", r.peer, r.got)
	}
}

func TestMeetingThroughAPrivateRelayAndDiscoveryServer(t *testing.T) {
	skipWithoutServers(t)
	discovery := startDiscovery(t)
	relay := startRelay(t)

	// The machine names its single relay and its discovery server, pinned by
	// the server's Device ID; the key holder needs only the discovery server,
	// and joins the relay the machine announced.
	machineCfg := machineConfig(t, relay, discovery.pinned())
	t.Run("a single relay, discovery pinned by id", func(t *testing.T) {
		meet(t, machineCfg, holderConfig(t, discovery.pinned()))
	})

	// ?insecure on the key holder's side instead of the id.
	t.Run("a single relay, key holder with ?insecure", func(t *testing.T) {
		meet(t, machineCfg, holderConfig(t, discovery.with("?insecure")))
	})

	// The same through a relay pool: a pool is an HTTP endpoint answering with
	// a list of relays.
	pool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"relays": []map[string]string{{"url": relay}}})
	}))
	defer pool.Close()
	poolCfg := machineConfig(t, pool.URL+"/endpoint", discovery.pinned())
	t.Run("a relay pool", func(t *testing.T) { meet(t, poolCfg, holderConfig(t, discovery.pinned())) })
}

// startMachine has a machine announce through cfg until the test ends, and
// waits until it can be found, so that a failure afterwards is about how the
// key holder trusts the server and not about timing. It returns the machine's
// Device ID.
func startMachine(t *testing.T, cfg transport.NetworkConfig, findWith transport.NetworkConfig) string {
	t.Helper()
	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	listenCtx, stopListening := context.WithCancel(context.Background())
	t.Cleanup(stopListening)
	tr := cfg.NewSyncthingRelay(machineCert)
	tr.AnnounceInterval = 200 * time.Millisecond
	go func() {
		for listenCtx.Err() == nil {
			if conn, err := tr.Listen(listenCtx, transport.ListenOptions{}); err == nil {
				conn.Close()
			}
		}
	}()

	probeCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "probe.pem"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the machine to announce", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := findWith.NewSyncthingRelay(probeCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
		if err == nil {
			conn.Close()
		}
		return err == nil || !strings.Contains(err.Error(), "no addresses announced")
	})
	return machineID
}

// Without the same discovery server on both sides the machine is not found,
// and the failure says what was tried, which is what an owner who set one
// side only needs to read.
func TestAKeyHolderOnAnotherDiscoveryServerDoesNotFindTheMachine(t *testing.T) {
	skipWithoutServers(t)
	machineDiscovery := startDiscovery(t)
	otherDiscovery := startDiscovery(t)
	relay := startRelay(t)

	machineID := startMachine(t, machineConfig(t, relay, machineDiscovery.pinned()), holderConfig(t, machineDiscovery.pinned()))

	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err = holderConfig(t, otherDiscovery.pinned()).NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err == nil {
		t.Fatal("the key holder reached a machine that announced somewhere else")
	}
	if !strings.Contains(err.Error(), "no addresses announced for peer") {
		t.Errorf("the failure should say the machine was not found there, got: %v", err)
	}
}

// A key holder told to expect a different server certificate than the one it
// meets must refuse, and say that the id does not match, even though the
// machine is announced and findable on that very server.
func TestAKeyHolderPinnedToTheWrongServerIDNeverConnects(t *testing.T) {
	skipWithoutServers(t)
	discovery := startDiscovery(t)
	relay := startRelay(t)
	machineID := startMachine(t, machineConfig(t, relay, discovery.pinned()), holderConfig(t, discovery.pinned()))

	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	wrong := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := holderConfig(t, discovery.with("?id="+wrong)).NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err == nil {
		conn.Close()
		t.Fatal("the key holder connected through a discovery server whose id it was not told")
	}
	for _, want := range []string{"id mismatch", discovery.id, wrong} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

// With neither ?id nor ?insecure the system's authorities decide, and a
// self-signed server is not one of them.
func TestAKeyHolderWithNeitherIDNorInsecureRefusesASelfSignedServer(t *testing.T) {
	skipWithoutServers(t)
	discovery := startDiscovery(t)
	relay := startRelay(t)
	machineID := startMachine(t, machineConfig(t, relay, discovery.pinned()), holderConfig(t, discovery.pinned()))

	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := holderConfig(t, discovery.url).NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err == nil {
		conn.Close()
		t.Fatal("the key holder trusted a self-signed server with no id and no ?insecure")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("the failure should be about the certificate, got: %v", err)
	}
}

// A machine told to expect a different server certificate than the one it
// meets must not announce there: the key holder, which looks the machine up on
// that very server with the right id, finds nothing.
func TestAMachinePinnedToTheWrongServerIDAnnouncesNothing(t *testing.T) {
	skipWithoutServers(t)
	discovery := startDiscovery(t)
	relay := startRelay(t)

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	wrong := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	tr := machineConfig(t, relay, discovery.with("?id="+wrong)).NewSyncthingRelay(machineCert)
	tr.AnnounceInterval = 200 * time.Millisecond
	listenCtx, stopListening := context.WithCancel(context.Background())
	defer stopListening()
	go func() { _, _ = tr.Listen(listenCtx, transport.ListenOptions{}) }()

	// Long enough for many announcement attempts (the interval is 200 ms).
	time.Sleep(4 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = holderConfig(t, discovery.pinned()).NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err == nil || !strings.Contains(err.Error(), "no addresses announced for peer") {
		t.Fatalf("Dial: %v, want the machine not to be found there", err)
	}
}
