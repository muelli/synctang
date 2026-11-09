// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muelli/synctang/internal/journal"
	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// fakeJournalSocket is a datagram socket in a short temporary directory standing in for
// /run/systemd/journal/socket; every datagram it receives is kept, decoded.
type fakeJournalSocket struct {
	path string
	conn *net.UnixConn
	mu   sync.Mutex
	raw  [][]byte
}

func newFakeJournalSocket(t *testing.T) *fakeJournalSocket {
	t.Helper()
	dir, err := os.MkdirTemp("", "jl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "socket")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeJournalSocket{path: path, conn: conn}
	go func() {
		buf := make([]byte, 1<<20)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.raw = append(f.raw, append([]byte(nil), buf[:n]...))
			f.mu.Unlock()
		}
	}()
	return f
}

func (f *fakeJournalSocket) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.raw)
}

func (f *fakeJournalSocket) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d datagrams arrived", f.count(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// entries decodes what arrived, in the native protocol's two forms.
func (f *fakeJournalSocket) entries(t *testing.T) []map[string]string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]string
	for _, p := range f.raw {
		m := map[string]string{}
		for len(p) > 0 {
			nl := bytes.IndexByte(p, '\n')
			eq := bytes.IndexByte(p, '=')
			if eq >= 0 && eq < nl {
				m[string(p[:eq])] = string(p[eq+1 : nl])
				p = p[nl+1:]
				continue
			}
			name := string(p[:nl])
			n := binary.LittleEndian.Uint64(p[nl+1 : nl+9])
			m[name] = string(p[nl+9 : nl+9+int(n)])
			p = p[nl+9+int(n)+1:]
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeJournalSocket) everything() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, p := range f.raw {
		b.Write(p)
		b.WriteByte('\n')
	}
	return b.String()
}

// fakeJournalConn is a connection to a fake journal that nothing reads.
func fakeJournalConn(t *testing.T) *journal.Conn {
	t.Helper()
	f := newFakeJournalSocket(t)
	c, err := journal.Dial(f.path, "synctang-unlocker")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAgentLogReachesTheJournalWithPrioritiesAndFields(t *testing.T) {
	f := newFakeJournalSocket(t)
	var console bytes.Buffer
	logger := newLogger(levelTrace, journalOn, f.path, &console)

	logger.Info("waiting for a key holder", "transport_id", "ABC-DEF")
	logger.Warn("announce failed", "error", "HTTP 500")
	logger.Error("recovery attempt failed", "error", "no key\nsecond line")
	logger.Log(context.Background(), levelTrace, "relay client created", "pool", "dynamic+https://pool")
	f.waitFor(t, 4)

	got := f.entries(t)
	for i, want := range []struct{ prio, msg string }{
		{"6", "waiting for a key holder"}, {"4", "announce failed"}, {"3", "recovery attempt failed"}, {"7", "relay client created"},
	} {
		if got[i]["PRIORITY"] != want.prio || got[i]["MESSAGE"] != want.msg || got[i]["SYSLOG_IDENTIFIER"] != "synctang-unlocker" {
			t.Errorf("entry %d: %v", i, got[i])
		}
	}
	if got[0]["TRANSPORT_ID"] != "ABC-DEF" || got[1]["ERROR"] != "HTTP 500" || got[2]["ERROR"] != "no key\nsecond line" {
		t.Errorf("fields: %v", got)
	}
	// The console keeps what a person at the machine needs: warnings and worse, not routine progress.
	if !strings.Contains(console.String(), "announce failed") || !strings.Contains(console.String(), "recovery attempt failed") {
		t.Errorf("console %q", console.String())
	}
	if strings.Contains(console.String(), "waiting for a key holder") || strings.Contains(console.String(), "relay client created") {
		t.Errorf("routine lines reached the console: %q", console.String())
	}
}

// No journal: everything goes to stderr, said once, and the agent is not stopped.
func TestAnUnavailableJournalIsSaidOnceAndLoggingGoesToTheConsole(t *testing.T) {
	var console bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no-socket")
	logger := newLogger(slog.LevelInfo, journalOn, missing, &console)
	logger.Info("first")
	logger.Info("second")
	out := console.String()
	if strings.Count(out, "journal") != 1 || !strings.Contains(out, "unavailable") {
		t.Errorf("the journal's absence should be said exactly once: %q", out)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("routine lines must reach the console without a journal: %q", out)
	}
}

func TestAutoUsesTheJournalOnlyWhenItsSocketExists(t *testing.T) {
	var console bytes.Buffer
	logger := newLogger(slog.LevelInfo, journalAuto, filepath.Join(t.TempDir(), "absent"), &console)
	logger.Info("hello")
	if !strings.Contains(console.String(), "hello") || strings.Contains(console.String(), "unavailable") {
		t.Errorf("auto with no journal should log to the console quietly: %q", console.String())
	}

	f := newFakeJournalSocket(t)
	var console2 bytes.Buffer
	newLogger(slog.LevelInfo, journalAuto, f.path, &console2).Info("to the journal")
	f.waitFor(t, 1)
	if strings.Contains(console2.String(), "to the journal") {
		t.Error("auto with a journal still sent routine lines to the console")
	}
}

func TestOffNeverTouchesTheJournal(t *testing.T) {
	f := newFakeJournalSocket(t)
	var console bytes.Buffer
	newLogger(slog.LevelInfo, journalOff, f.path, &console).Error("loud")
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Error("--journal=off sent to the journal")
	}
	if !strings.Contains(console.String(), "loud") {
		t.Error("and it did not log to the console")
	}
}

func TestAJournalThatGoesAwayIsSaidOnceAndTheConsoleTakesOver(t *testing.T) {
	f := newFakeJournalSocket(t)
	var console bytes.Buffer
	logger := newLogger(slog.LevelInfo, journalOn, f.path, &console)
	logger.Info("while it worked")
	f.waitFor(t, 1)
	f.conn.Close()
	os.Remove(f.path)
	logger.Info("after")
	logger.Info("and after")
	out := console.String()
	if strings.Count(out, "journal") != 1 {
		t.Errorf("said %d times: %q", strings.Count(out, "journal"), out)
	}
	if !strings.Contains(out, "after") || !strings.Contains(out, "and after") {
		t.Errorf("the console did not take over: %q", out)
	}
}

func TestParseJournalMode(t *testing.T) {
	for in, want := range map[string]journalMode{"auto": journalAuto, "on": journalOn, "off": journalOff, "": journalAuto} {
		got, err := parseJournalMode(in)
		if err != nil || got != want {
			t.Errorf("%q: %v, %v", in, got, err)
		}
	}
	if _, err := parseJournalMode("maybe"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestOnlyTheAgentHasAJournalFlag(t *testing.T) {
	for _, command := range []string{"pair", "open", "enrol", "status"} {
		if strings.Contains(helpOf(t, command), "-journal") {
			t.Errorf("%s has a --journal flag; only the agent logs to the journal", command)
		}
	}
	if !strings.Contains(helpOf(t, "agent"), "-journal") {
		t.Error("the agent has no --journal flag")
	}
}

// Pairing at trace level, with everything going to a journal and a console: the one-time
// code, the secret behind it, the passphrase and the public key never appear in either.
func TestPairingAtTraceLevelLogsNoSecret(t *testing.T) {
	f := newPairFixture(t)
	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	js := newFakeJournalSocket(t)
	var console bytes.Buffer
	logger := newLogger(levelTrace, journalOn, js.path, &console)

	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		errCh <- runPairOnce(ctx, pairOptions{
			device: f.device, existingPassphrase: f.passphrase, machineCert: f.machineCert, machineName: "test-machine",
			secret: secret, confirm: func(string, []byte, string) bool { return true },
			listenOpts: transport.ListenOptions{DirectAddr: f.addr}, logger: logger,
		})
	}()
	result, err := dialAndPair(t, f, f.addr, secret)
	if err != nil || !result.OK {
		t.Fatalf("pairing: %+v, %v", result, err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	// A refused attempt too: the wrong code is a thing that gets logged.
	logger.Warn("a pairing attempt was refused", "peer", f.holderID)
	time.Sleep(100 * time.Millisecond)

	logged := js.everything() + "\n" + console.String()
	if !strings.Contains(logged, "transport") && !strings.Contains(logged, "handshake") && !strings.Contains(logged, "refused") {
		t.Fatalf("nothing was logged at trace level:\n%s", logged)
	}
	for name, secretText := range map[string]string{
		"the pairing code":        code,
		"the secret as hex":       hex.EncodeToString(secret),
		"the secret, raw":         string(secret),
		"the passphrase":          string(f.passphrase),
		"the key holder's public": hex.EncodeToString(f.holderS),
		"the key holder's scalar": hex.EncodeToString(f.holderS_),
	} {
		if strings.Contains(logged, secretText) {
			t.Errorf("%s was logged", name)
		}
	}
}
