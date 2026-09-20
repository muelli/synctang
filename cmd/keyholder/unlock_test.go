// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
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

// TestUnlockAnswersMachineRecovery is the keyholder-side half of the
// end-to-end recovery test: cmd/unlocker/agent_test.go already covers the
// machine side, driving a simulated key holder with direct mrcore calls.
// This test drives the actual keyholder unlock code (runUnlockOnce, the
// same function "keyholder unlock" runs) against a simulated machine side
// written the same way, mirroring mrcore/mr1_test.go's own enrol/recover
// round trip. cmd/unlocker and cmd/keyholder stay two independent
// binaries; nothing here imports the other package.
func TestUnlockAnswersMachineRecovery(t *testing.T) {
	g := mrcore.P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	// Play the machine's part in enrolment directly, the same way a real
	// "unlocker enrol" would via mrcore.Enrol: this is what a Recipient
	// carrying the key holder's own public key S looks like on disk.
	P, rec, err := mrcore.Enrol(g, S)
	if err != nil {
		t.Fatalf("mrcore.Enrol: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	recoveredCh := make(chan []byte, 1)
	machineErrCh := make(chan error, 1)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			machineErrCh <- err
			return
		}
		defer conn.Close()

		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: "sim-machine"}); err != nil {
			machineErrCh <- err
			return
		}
		// No confirm code required: an empty Code tells the key holder to
		// skip both its console prompt and sending a response back.
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""}); err != nil {
			machineErrCh <- err
			return
		}

		e, X, err := mrcore.ChallengeStart(g, rec.C)
		if err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: rec.Kid, X: X}); err != nil {
			machineErrCh <- err
			return
		}

		var resp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			machineErrCh <- err
			return
		}
		gotP, err := mrcore.FinishFullPoint(g, e, rec, resp.Y)
		if err != nil {
			machineErrCh <- err
			return
		}
		// The real machine reports the outcome as its last message,
		// and a key holder now waits for it. A fake that skips it is a
		// fake that does not behave like the thing under test, which
		// is how the app's confirm-code bug survived every test.
		if err := mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true}); err != nil {
			machineErrCh <- err
			return
		}
		recoveredCh <- gotP
		machineErrCh <- nil
	}()

	var stdout strings.Builder
	if err := runUnlockOnce(ctx, s, keyholderCert, machineID, addr, true, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("runUnlockOnce: %v", err)
	}

	if err := <-machineErrCh; err != nil {
		t.Fatalf("simulated machine side: %v", err)
	}
	gotP := <-recoveredCh
	if !bytes.Equal(gotP, P) {
		t.Fatalf("the machine recovered the wrong secret:\n got=%x\nwant=%x", gotP, P)
	}
	if !strings.Contains(stdout.String(), "unlocked") {
		t.Errorf("expected runUnlockOnce to report success, got %q", stdout.String())
	}
}

// A non-empty ConfirmCodeChallenge must make runUnlockOnce prompt for a
// code on stdin and relay back exactly what was typed, trimmed.
func TestUnlockRelaysConfirmCode(t *testing.T) {
	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	_, rec, err := mrcore.Enrol(g, S)
	if err != nil {
		t.Fatalf("mrcore.Enrol: %v", err)
	}

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	keyholderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatalf("keyholder LoadOrCreateCert: %v", err)
	}

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const wantCode = "654321"
	gotCodeCh := make(chan string, 1)
	machineErrCh := make(chan error, 1)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)
		conn, err := tr.Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			machineErrCh <- err
			return
		}
		defer conn.Close()

		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: "sim-machine"}); err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: wantCode}); err != nil {
			machineErrCh <- err
			return
		}
		var resp mrcore.ConfirmCodeResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			machineErrCh <- err
			return
		}
		gotCodeCh <- resp.Code

		_, X, err := mrcore.ChallengeStart(g, rec.C)
		if err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: rec.Kid, X: X}); err != nil {
			machineErrCh <- err
			return
		}
		var recoverResp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &recoverResp); err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true}); err != nil {
			machineErrCh <- err
			return
		}
		machineErrCh <- nil
	}()

	var stdout strings.Builder
	stdin := strings.NewReader(wantCode + "\n")
	if err := runUnlockOnce(ctx, s, keyholderCert, machineID, addr, true, stdin, &stdout); err != nil {
		t.Fatalf("runUnlockOnce: %v", err)
	}

	if err := <-machineErrCh; err != nil {
		t.Fatalf("simulated machine side: %v", err)
	}
	if gotCode := <-gotCodeCh; gotCode != wantCode {
		t.Fatalf("relayed confirm code = %q, want %q", gotCode, wantCode)
	}
}

// Found running unlock against a real deployment: Multi races
// LocalDiscovery against SyncthingRelay independently on the machine
// and the key holder, so the two sides can each pick a different
// winning transport for what was meant to be one attempt, producing a
// connection that completes its handshake and then reads EOF (nobody
// on the other end ever writes to it). "run unlock" (the CLI entry
// point, not runUnlockOnce directly) must retry the whole attempt
// rather than give up: a machine listening again on a second attempt
// still gets a real key holder to answer it.
func TestRunUnlockRetriesAfterATransientFailure(t *testing.T) {
	g := mrcore.P256()

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	s, err := generateAndSave(keyFile, false)
	if err != nil {
		t.Fatalf("generateAndSave: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	P, rec, err := mrcore.Enrol(g, S)
	if err != nil {
		t.Fatalf("mrcore.Enrol: %v", err)
	}
	transportKeyFile := filepath.Join(dir, "transport.pem")

	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatalf("machine LoadOrCreateCert: %v", err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()

	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	recoveredCh := make(chan []byte, 1)
	machineErrCh := make(chan error, 1)
	go func() {
		tr := transport.NewSyncthingRelay(machineCert)

		// First attempt: accept, complete the handshake, then hang up
		// without writing anything, exactly like a losing Multi race
		// that still finished connecting.
		conn, err := tr.Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			machineErrCh <- err
			return
		}
		conn.Close()

		// Second attempt: behave correctly.
		conn, err = tr.Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			machineErrCh <- err
			return
		}
		defer conn.Close()

		if err := mrcore.WriteMessage(conn, mrcore.Hello{Name: "sim-machine"}); err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.ConfirmCodeChallenge{Code: ""}); err != nil {
			machineErrCh <- err
			return
		}

		e, X, err := mrcore.ChallengeStart(g, rec.C)
		if err != nil {
			machineErrCh <- err
			return
		}
		if err := mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: rec.Kid, X: X}); err != nil {
			machineErrCh <- err
			return
		}

		var resp mrcore.RecoverResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			machineErrCh <- err
			return
		}
		gotP, err := mrcore.FinishFullPoint(g, e, rec, resp.Y)
		if err != nil {
			machineErrCh <- err
			return
		}
		// The real machine reports the outcome as its last message,
		// and a key holder now waits for it. A fake that skips it is a
		// fake that does not behave like the thing under test, which
		// is how the app's confirm-code bug survived every test.
		if err := mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true}); err != nil {
			machineErrCh <- err
			return
		}
		recoveredCh <- gotP
		machineErrCh <- nil
	}()

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"unlock",
		"--key-file", keyFile,
		"--transport-key-file", transportKeyFile,
		"--direct-addr", addr,
		"--yes",
		"--timeout", "15s",
		machineID,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run unlock: exit %d, stderr %q", code, stderr.String())
	}

	if err := <-machineErrCh; err != nil {
		t.Fatalf("simulated machine side: %v", err)
	}
	gotP := <-recoveredCh
	if !bytes.Equal(gotP, P) {
		t.Fatalf("the machine recovered the wrong secret:\n got=%x\nwant=%x", gotP, P)
	}
	if !strings.Contains(stdout.String(), "unlocked") {
		t.Errorf("expected run unlock to report success, got %q", stdout.String())
	}
}
