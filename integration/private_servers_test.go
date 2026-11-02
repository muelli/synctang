// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integration holds tests that need programs this module does not
// build itself. They skip when those are not there.
//
// private_servers_test.go runs a machine and a key holder through a relay
// server and a discovery server of their own, on the loopback, with the real
// Syncthing programs strelaysrv and stdiscosrv rather than fakes of them.
// Nothing in it is pointed at the public Syncthing servers: the settings under
// test are exactly what keeps it from doing so, and a lookup that went there
// instead would not find the machine. It does not, however, take the network
// away, so that is argued from the settings and not enforced.
//
// The servers are reached on this host's own non-loopback address, not on
// 127.0.0.1: stdiscosrv silently drops loopback addresses from what it is
// told, so a relay announced as relay://127.0.0.1:... is never stored and the
// machine is never found. A private deployment is on a LAN address anyway.
// The test skips on a host with none.
//
// To run it, build the two servers somewhere outside this module (their
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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

// discoCertPEM and discoKeyPEM are the private discovery server's own
// certificate, for this host's addresses. The lookup side verifies the server's
// certificate against the system roots (there is no way to hand it another
// pool, and private CAs are out of scope for the product), so TestMain points
// SSL_CERT_FILE at this certificate before anything has loaded the roots,
// which is why this lives in a package of its own.
var discoCertPEM, discoKeyPEM []byte

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

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "synctang-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "private discovery"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	if ip := hostIP(); ip != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	discoCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	discoKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	roots := filepath.Join(dir, "roots.pem")
	if err := os.WriteFile(roots, discoCertPEM, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("SSL_CERT_FILE", roots)
	os.Setenv("SSL_CERT_DIR", dir)

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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

// startDiscovery runs stdiscosrv on the loopback and returns its URL.
func startDiscovery(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("STDISCOSRV")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), discoCertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), discoKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := net.JoinHostPort(hostIP().String(), strconv.Itoa(port))
	out := start(t, bin,
		"--cert", filepath.Join(dir, "cert.pem"), "--key", filepath.Join(dir, "key.pem"),
		"--listen", addr, "--db-dir", dir)
	waitFor(t, "the discovery server to listen\n"+out.String(), func() bool {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	return fmt.Sprintf("https://%s/v2/", addr)
}

var relayURIRe = regexp.MustCompile(`URI: (relay://\S+)`)

// startRelay runs strelaysrv on the loopback, joined to no pool, and returns
// the relay:// URI it prints, which carries its Device ID.
func startRelay(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("STRELAYSRV")
	port := freePort(t)
	out := start(t, bin,
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

// meet has the machine listen through machineCfg and the key holder dial
// through keyholderCfg, and checks that bytes cross both ways and that each
// side saw the other's real identity.
func meet(t *testing.T, machineCfg, keyholderCfg transport.NetworkConfig) {
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
		conn, err := machineTr.Listen(ctx, transport.ListenOptions{AuthorizedPeers: []string{holderID}})
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

	conn, err := keyholderCfg.NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
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

	// The machine names its single relay and its discovery server; the key
	// holder needs only the discovery server, and joins the relay the machine
	// announced.
	machineCfg, err := transport.ParseNetworkConfig(relay, discovery, "")
	if err != nil {
		t.Fatal(err)
	}
	holderCfg, err := transport.ParseNetworkConfig("", discovery, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a single relay", func(t *testing.T) { meet(t, machineCfg, holderCfg) })

	// The same through a relay pool: a pool is an HTTP endpoint answering with
	// a list of relays.
	pool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"relays": []map[string]string{{"url": relay}}})
	}))
	defer pool.Close()
	poolCfg, err := transport.ParseNetworkConfig(pool.URL+"/endpoint", discovery, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a relay pool", func(t *testing.T) { meet(t, poolCfg, holderCfg) })
}

// Without the same discovery server on both sides the machine is not found,
// and the failure says what was tried, which is what an owner who set one
// side only needs to read.
func TestAKeyHolderOnAnotherDiscoveryServerDoesNotFindTheMachine(t *testing.T) {
	skipWithoutServers(t)
	machineDiscovery := startDiscovery(t)
	otherDiscovery := startDiscovery(t)
	relay := startRelay(t)

	machineCfg, err := transport.ParseNetworkConfig(relay, machineDiscovery, "")
	if err != nil {
		t.Fatal(err)
	}
	wrongCfg, err := transport.ParseNetworkConfig("", otherDiscovery, "")
	if err != nil {
		t.Fatal(err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	listenCtx, stopListening := context.WithCancel(context.Background())
	defer stopListening()
	machineTr := machineCfg.NewSyncthingRelay(machineCert)
	machineTr.AnnounceInterval = 200 * time.Millisecond
	go func() { _, _ = machineTr.Listen(listenCtx, transport.ListenOptions{}) }()

	// Wait until the machine is findable where it announced, so that the
	// failure below is about the server and not about timing.
	found := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := transport.ParseNetworkConfig("", machineDiscovery, "")
		if err != nil {
			t.Fatal(err)
		}
		_ = c
		conn, err := c.NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
		if err == nil {
			conn.Close()
		}
		return err == nil || !strings.Contains(err.Error(), "no addresses announced")
	}
	waitFor(t, "the machine to announce", found)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err = wrongCfg.NewSyncthingRelay(holderCert).Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err == nil {
		t.Fatal("the key holder reached a machine that announced somewhere else")
	}
	if !strings.Contains(err.Error(), "no addresses announced for peer") {
		t.Errorf("the failure should say the machine was not found there, got: %v", err)
	}
}
