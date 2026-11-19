// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A machine that says it will do something other than unlock with the answer is said so to the person approving:
// the answer is the passphrase of this key holder's own keyslot, and the machine can do with it anything a LUKS
// passphrase can do. An unlock prompt that says "wants to unlock" for an enrolment, and a result that says
// "unlocked" after one, would be false. The purpose is the machine's own word, shown as a warning, never as
// "this is only an unlock".

// unlockGreeting runs one unlock against a simulated machine that greets with purpose, and returns what the
// key holder printed. stdin is what the person types.
func unlockGreeting(t *testing.T, purpose string, yes bool, stdin string) string {
	t.Helper()
	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatal(err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatal(err)
	}
	_, rec, err := mrcore.Enrol(g, S)
	if err != nil {
		t.Fatal(err)
	}
	machineCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	machineID := protocol.NewDeviceID(machineCert.Certificate[0]).String()
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	addr := freeTestAddr(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		conn, err := transport.NewSyncthingRelay(machineCert).Listen(ctx, transport.ListenOptions{DirectAddr: addr})
		if err != nil {
			return
		}
		defer conn.Close()
		_ = mrcore.WriteMessage(conn, mrcore.Hello{Name: "sim-machine", Purpose: purpose})
		_, X, _ := mrcore.ChallengeStart(g, rec.C)
		_ = mrcore.WriteMessage(conn, mrcore.RecoverRequest{Kid: rec.Kid, X: X})
		var resp mrcore.RecoverResponse
		if mrcore.ReadMessage(conn, &resp) != nil {
			return
		}
		_ = mrcore.WriteMessage(conn, mrcore.RecoverResult{OK: true})
		time.Sleep(300 * time.Millisecond)
	}()
	var stdout strings.Builder
	if err := runUnlockOnce(ctx, s, holderCert, machineID, "", addr, transport.NetworkConfig{}, yes, strings.NewReader(stdin), &stdout, func() {}); err != nil {
		t.Fatalf("runUnlockOnce: %v\n%s", err, stdout.String())
	}
	return stdout.String()
}

func TestAMachineThatWillOnlyUnlockIsApprovedAsAnUnlockWithTheStandingNoticeAbove(t *testing.T) {
	out := unlockGreeting(t, "", false, "\n")
	if !strings.Contains(out, "wants to unlock") || !strings.Contains(out, "unlocked") {
		t.Errorf("%s", out)
	}
	// True of every approval: the machine gets the passphrase of this key holder's keyslot.
	if !strings.Contains(out, "passphrase of this key holder's keyslot") || strings.Contains(out, "not an unlock") {
		t.Errorf("the standing notice is missing, or an unlock is called something else:\n%s", out)
	}
}

func TestAMachineThatWillEnrolSaysItIsNotAnUnlockAndTheResultIsNotCalledUnlocked(t *testing.T) {
	for purpose, words := range map[string]string{
		mrcore.PurposeEnrol:   "enrol a key holder",
		mrcore.PurposePair:    "pair a key holder",
		mrcore.PurposeExtract: "print the passphrase",
	} {
		out := unlockGreeting(t, purpose, false, "\n")
		for _, want := range []string{"not an unlock", words, "passphrase of this key holder's keyslot", "Press Enter to approve"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the output lacks %q:\n%s", purpose, want, out)
			}
		}
		if strings.Contains(out, "wants to unlock") || strings.Contains(out, "unlocked") {
			t.Errorf("%s: it is called an unlock:\n%s", purpose, out)
		}
		if !strings.Contains(out, "approved") {
			t.Errorf("%s: the result does not say what was approved:\n%s", purpose, out)
		}
	}
}

// --yes is an answer given in advance, and it is still told what it is for.
func TestYesStillSaysWhatTheApprovalIsFor(t *testing.T) {
	out := unlockGreeting(t, mrcore.PurposeEnrol, true, "")
	if !strings.Contains(out, "not an unlock") || strings.Contains(out, "Press Enter") || strings.Contains(out, "unlocked") {
		t.Errorf("%s", out)
	}
}

// A purpose this program does not know is a purpose all the same: something other than an unlock, shown cleaned.
func TestAPurposeThisProgramDoesNotKnowIsStillNotAnUnlock(t *testing.T) {
	out := unlockGreeting(t, "reformat\x1b[31m everything\n", true, "")
	if !strings.Contains(out, "not an unlock") || !strings.Contains(out, "reformat") || strings.Contains(out, "\x1b[31m") || strings.Contains(out, "unlocked") {
		t.Errorf("%q", out)
	}
}
