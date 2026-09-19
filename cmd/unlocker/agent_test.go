// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// freeTestAddr reserves an ephemeral TCP port on loopback and hands back
// its address, closing the probe listener straight away. Mirrors
// mrcore/transport's own freeAddr test helper; duplicated here rather than
// exported since it is only a few lines.
func freeTestAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing probe listener: %v", err)
	}
	return addr
}

// writeAskRequest creates a fake /run/systemd/ask-password-shaped request
// file in dir, naming a Unix datagram socket this test itself listens on,
// and returns that socket so the test can read the reply off it.
func writeAskRequest(t *testing.T, dir string) net.PacketConn {
	t.Helper()

	sockPath := filepath.Join(dir, "reply.sock")
	pc, err := net.ListenPacket("unixgram", sockPath)
	if err != nil {
		t.Fatalf("listening on fake ask-password socket: %v", err)
	}
	t.Cleanup(func() { pc.Close() })

	contents := "[Ask]\nSocket=" + sockPath + "\nId=cryptsetup:test\n"
	if err := os.WriteFile(filepath.Join(dir, "ask.test"), []byte(contents), 0o600); err != nil {
		t.Fatalf("writing fake ask-password request: %v", err)
	}
	return pc
}

// T3.2: the agent runs a full recovery exchange against a file-backend-
// style key holder dialing in over DirectAddr, and answers the matching
// ask-password request with exactly "+" followed by the correct P (P's
// correctness is checked the same way enrol_test.go checks its own work:
// by actually unlocking the loopback image with it).
func TestAgentRecoversAndAnswersAskPassword(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", keyholderID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	askDir := t.TempDir()
	replyConn := writeAskRequest(t, askDir)

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr := transport.NewSyncthingRelay(machineCert)
	agentErrCh := make(chan error, 1)
	go func() {
		agentErrCh <- runAgentOnce(ctx, device, machineCert, "test-machine", "", askDir,
			transport.ListenOptions{DirectAddr: addr}, tr)
	}()

	keyholderTr := transport.NewSyncthingRelay(keyholderCert)
	conn, err := keyholderTr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("keyholder Dial: %v", err)
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading Hello: %v", err)
	}
	if hello.Name != "test-machine" {
		t.Errorf("Hello.Name = %q, want %q", hello.Name, "test-machine")
	}

	var challenge mrcore.ConfirmCodeChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		t.Fatalf("reading ConfirmCodeChallenge: %v", err)
	}
	if challenge.Code != "" {
		t.Fatalf("ConfirmCodeChallenge.Code = %q, want empty: confirm-code is not active in this test", challenge.Code)
	}

	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		t.Fatalf("reading RecoverRequest: %v", err)
	}

	Y, err := g.ScalarMult(req.X, s)
	if err != nil {
		t.Fatalf("ScalarMult: %v", err)
	}
	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		t.Fatalf("sending RecoverResponse: %v", err)
	}

	if err := <-agentErrCh; err != nil {
		t.Fatalf("runAgentOnce: %v", err)
	}

	buf := make([]byte, 4096)
	if err := replyConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, _, err := replyConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("reading ask-password reply: %v", err)
	}
	got := buf[:n]
	if len(got) == 0 || got[0] != '+' {
		t.Fatalf("ask-password reply = %q, want a leading '+'", got)
	}
	P := got[1:]

	if _, err := runCommand(P, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", device); err != nil {
		t.Fatalf("the delivered secret does not open %s: %v", device, err)
	}
}

// multicastOrSkip skips the test if this environment does not deliver a
// self-addressed multicast packet: some sandboxes restrict it even on a
// machine's own interfaces, and the point of this test is to prove local
// discovery works where it is available, not to fail on infrastructure
// that does not support it at all.
func multicastOrSkip(t *testing.T) {
	t.Helper()
	group := &net.UDPAddr{IP: net.ParseIP("239.255.42.100"), Port: 21077}

	recv, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer recv.Close()
	if err := recv.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	send, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
	defer send.Close()
	if _, err := send.Write([]byte("probe")); err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}

	buf := make([]byte, 16)
	if _, _, err := recv.ReadFromUDP(buf); err != nil {
		t.Skipf("multicast not available in this environment: %v", err)
	}
}

// A9 requirement made concrete for the offline case: unlock must work
// with no relay and no discovery server involved at all, over
// transport.LocalDiscovery alone, exactly as it does over
// SyncthingRelay's DirectAddr in the test above. This is the actual
// behaviour "unlock works even without the Internet" means end to end,
// not just at the transport layer LocalDiscovery's own tests already
// cover.
func TestAgentRecoversOverLocalDiscovery(t *testing.T) {
	multicastOrSkip(t)

	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", keyholderID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	askDir := t.TempDir()
	replyConn := writeAskRequest(t, askDir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tr := &transport.LocalDiscovery{Cert: machineCert, AnnounceInterval: 50 * time.Millisecond}
	agentErrCh := make(chan error, 1)
	go func() {
		agentErrCh <- runAgentOnce(ctx, device, machineCert, "test-machine", "", askDir,
			transport.ListenOptions{}, tr)
	}()

	keyholderTr := &transport.LocalDiscovery{Cert: keyholderCert}
	conn, err := keyholderTr.Dial(ctx, transport.DialOptions{PeerID: machineID})
	if err != nil {
		t.Fatalf("keyholder Dial: %v", err)
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading Hello: %v", err)
	}

	var challenge mrcore.ConfirmCodeChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		t.Fatalf("reading ConfirmCodeChallenge: %v", err)
	}

	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		t.Fatalf("reading RecoverRequest: %v", err)
	}

	Y, err := g.ScalarMult(req.X, s)
	if err != nil {
		t.Fatalf("ScalarMult: %v", err)
	}
	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		t.Fatalf("sending RecoverResponse: %v", err)
	}

	if err := <-agentErrCh; err != nil {
		t.Fatalf("runAgentOnce: %v", err)
	}

	buf := make([]byte, 4096)
	if err := replyConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, _, err := replyConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("reading ask-password reply: %v", err)
	}
	got := buf[:n]
	if len(got) == 0 || got[0] != '+' {
		t.Fatalf("ask-password reply = %q, want a leading '+'", got)
	}
	P := got[1:]

	if _, err := runCommand(P, "cryptsetup", "open", "--test-passphrase",
		"--key-file=-", device); err != nil {
		t.Fatalf("the delivered secret does not open %s: %v", device, err)
	}
}

// T3.3: a confirm code response that does not match the code the agent
// generated must be rejected before any RecoverResponse is even trusted,
// and no ask-password reply must ever be sent.
func TestAgentRejectsWrongConfirmCode(t *testing.T) {
	existingPassphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, existingPassphrase)

	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}
	keyholderID := protocol.NewDeviceID(keyholderCert.Certificate[0]).String()

	if err := enrolRecipient(device, existingPassphrase, S, "test-machine", "MACHINE-ID", keyholderID); err != nil {
		t.Fatalf("enrolRecipient: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	askDir := t.TempDir()
	replyConn := writeAskRequest(t, askDir)

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const wantCode = "123456"
	tr := transport.NewSyncthingRelay(machineCert)
	agentErrCh := make(chan error, 1)
	go func() {
		agentErrCh <- runAgentOnce(ctx, device, machineCert, "test-machine", wantCode, askDir,
			transport.ListenOptions{DirectAddr: addr}, tr)
	}()

	keyholderTr := transport.NewSyncthingRelay(keyholderCert)
	conn, err := keyholderTr.Dial(ctx, transport.DialOptions{PeerID: machineID, DirectAddr: addr})
	if err != nil {
		t.Fatalf("keyholder Dial: %v", err)
	}
	defer conn.Close()

	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading Hello: %v", err)
	}

	var challenge mrcore.ConfirmCodeChallenge
	if err := mrcore.ReadMessage(conn, &challenge); err != nil {
		t.Fatalf("reading ConfirmCodeChallenge: %v", err)
	}
	if challenge.Code != wantCode {
		t.Fatalf("ConfirmCodeChallenge.Code = %q, want %q", challenge.Code, wantCode)
	}

	if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeResponse{Code: "000000"}); err != nil {
		t.Fatalf("sending ConfirmCodeResponse: %v", err)
	}

	if err := <-agentErrCh; err == nil {
		t.Fatal("runAgentOnce: expected an error for a wrong confirm code, got nil")
	}

	buf := make([]byte, 4096)
	if err := replyConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, _, err := replyConn.ReadFrom(buf); err == nil {
		t.Fatal("ask-password socket received a reply despite the wrong confirm code")
	}
}
