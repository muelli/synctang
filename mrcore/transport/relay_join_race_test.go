// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/relay/client"
	relayprotocol "github.com/syncthing/syncthing/lib/relay/protocol"
)

// strelaysrv hands a session's second connection to the goroutine that serves the session with a non-blocking send
// on an unbuffered channel (cmd/strelaysrv/session.go, AddConnection). If the second party joins while that
// goroutine is still busy with the first, the send finds nobody receiving and the relay answers "already
// connected" to a session that is not full, and has by then used up the key, so it cannot be tried again. The
// relay invites both parties at the same moment, and on a fast relay they join within microseconds of one
// another: measured against strelaysrv 1.30.0, one join in about eight was refused when both went at once, and
// none when the key holder waited twenty milliseconds. The party that was not refused sits in a session nobody
// else will join until its TLS handshake times out, which is how a meeting through a private relay failed one
// run in ten with "client handshake: i/o timeout" or "joining session: already connected".

// Upstream issue: https://github.com/syncthing/syncthing/issues/10952

var relayURIPattern = regexp.MustCompile(`URI: (relay://\S+)`)

func hostIPv4() net.IP {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP
		}
	}
	return nil
}

// startTestRelay runs strelaysrv, joined to no pool, and returns its URI.
func startTestRelay(t *testing.T, bin string) *url.URL {
	t.Helper()
	host := hostIPv4()
	if host == nil {
		t.Skip("this host has no non-loopback IPv4 address")
	}
	ln, err := net.Listen("tcp", host.String()+":0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	var out lockedBuffer
	cmd := exec.Command(bin, "-keys", t.TempDir(), "-listen", net.JoinHostPort(host.String(), strconv.Itoa(port)), "-pools=", "-status-srv=")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting strelaysrv: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for {
		if m := relayURIPattern.FindStringSubmatch(out.String()); m != nil {
			u, err := url.Parse(m[1])
			if err != nil {
				t.Fatal(err)
			}
			return u
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay printed no URI:\n%s", out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestTheKeyHolderJoinsAfterTheMachineSoTheRelayDoesNotRefuseAJoinThatIsNotRefusable(t *testing.T) {
	bin := os.Getenv("STRELAYSRV")
	if bin == "" {
		t.Skip("set STRELAYSRV to a strelaysrv binary (see integration/private_servers_test.go)")
	}
	relay := startTestRelay(t, bin)
	machineCert, err := LoadOrCreateCert(t.TempDir() + "/machine.pem")
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := LoadOrCreateCert(t.TempDir() + "/holder.pem")
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The machine's control connection, as listenRelay keeps it.
	control, err := client.NewClient(relay, []tls.Certificate{machineCert}, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go control.Serve(ctx)
	holder := &SyncthingRelay{Cert: holderCert}

	// The first invitation can only be asked for once the machine has joined.
	var first bool
	for deadline := time.Now().Add(10 * time.Second); !first && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		_, err := client.GetInvitationFromRelay(ctx, relay, machineID, []tls.Certificate{holderCert}, 5*time.Second)
		first = err == nil
		if first {
			<-control.Invitations()
		}
	}
	if !first {
		t.Fatal("the machine never joined the relay")
	}

	const rounds = 80
	var refused []string
	for i := 0; i < rounds; i++ {
		holderInvitation, err := client.GetInvitationFromRelay(ctx, relay, machineID, []tls.Certificate{holderCert}, 10*time.Second)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		machineInvitation := <-control.Invitations()
		var wg sync.WaitGroup
		var machineErr, holderErr error
		var machineConn, holderConn net.Conn
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			machineConn, machineErr = holder.joinRelaySessionAsMachine(ctx, machineInvitation)
		}()
		go func() {
			defer wg.Done()
			<-start
			holderConn, holderErr = holder.joinRelaySession(ctx, holderInvitation)
		}()
		close(start)
		wg.Wait()
		for who, err := range map[string]error{"machine": machineErr, "key holder": holderErr} {
			if err != nil && strings.Contains(err.Error(), "already connected") {
				refused = append(refused, who)
			} else if err != nil {
				t.Fatalf("round %d: %s: %v", i, who, err)
			}
		}
		if machineConn != nil {
			machineConn.Close()
		}
		if holderConn != nil {
			holderConn.Close()
		}
	}
	if len(refused) > 0 {
		t.Errorf("the relay answered \"already connected\" to %d of %d joins that were not refusable (%v)", len(refused), 2*rounds, refused)
	}
}

// The wiring: the machine joins after its short delay and the key holder after its longer one, each before it
// touches the relay.
func TestEachSideWaitsItsOwnTimeBeforeItJoinsARelaySession(t *testing.T) {
	var waits []time.Duration
	relay := &SyncthingRelay{sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
	nowhere := relayprotocol.SessionInvitation{Address: net.IPv4(127, 0, 0, 1), Port: 1}
	_, _ = relay.joinRelaySession(context.Background(), nowhere)
	_, _ = relay.joinRelaySessionAsMachine(context.Background(), nowhere)
	if len(waits) != 2 || waits[0] != relayJoinStagger || waits[1] != relayMachineJoinDelay {
		t.Errorf("waited %v, want the key holder's %s and then the machine's %s", waits, relayJoinStagger, relayMachineJoinDelay)
	}
	if relayJoinStagger-relayMachineJoinDelay < 20*time.Millisecond {
		t.Errorf("the two sides are only %s apart; twenty milliseconds is what kept the relay from refusing a join", relayJoinStagger-relayMachineJoinDelay)
	}
}
