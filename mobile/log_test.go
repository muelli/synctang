// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// lockedBuffer is a bytes.Buffer the logger and the test can use at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLog sends the bridge's log to a buffer for the length of the test and puts
// the default level back after it.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	setLogOutput(buf)
	t.Cleanup(func() {
		setLogOutput(nil)
		_ = SetLogLevel("warn")
	})
	return buf
}

func TestSetLogLevelAcceptsTheKnownLevelsAndRefusesTheRest(t *testing.T) {
	t.Cleanup(func() { _ = SetLogLevel("warn") })
	for _, level := range []string{"trace", "debug", "info", "warn", "error"} {
		if err := SetLogLevel(level); err != nil {
			t.Errorf("%q refused: %v", level, err)
		}
	}
	// No silent fallback: a level that is not one of those is an error, and changes nothing.
	_ = SetLogLevel("debug")
	for _, bad := range []string{"", "verbose", "TRACE ", "warning", "0"} {
		err := SetLogLevel(bad)
		if err == nil || !strings.Contains(err.Error(), "trace, debug, info, warn, error") {
			t.Errorf("%q: %v, want an error naming the levels", bad, err)
		}
	}
	buf := captureLog(t)
	_ = SetLogLevel("debug")
	_ = SetLogLevel("nonsense")
	bridgeLog().Debug("still debug")
	if !strings.Contains(buf.String(), "still debug") {
		t.Error("a refused level changed the level")
	}
}

// A release build asks for warn and gets no trace lines: a whole connect with its stages
// and attempts says nothing.
func TestTheDefaultLevelEmitsNoTraceLines(t *testing.T) {
	buf := captureLog(t)
	runSessionExchange(t, "default level phone", "default level machine")
	if buf.String() != "" {
		t.Errorf("the default level logged:\n%s", buf.String())
	}
}

func TestTraceLogsEveryStageChangeWithFromToAndElapsed(t *testing.T) {
	buf := captureLog(t)
	if err := SetLogLevel("trace"); err != nil {
		t.Fatal(err)
	}
	runSessionExchange(t, "trace level phone", "trace level machine")
	out := buf.String()
	for _, want := range []string{
		"stage", "from=starting", "to=connecting", "to=checking_identity", "to=waiting_for_request",
		"to=request_ready", "to=sending_answer", "to=waiting_for_verdict", "to=unlocked",
		"elapsed_ms=", "attempt=1", "route=direct", "level=", "time=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not contain %q:\n%s", want, out)
		}
	}
	// In order: the answer is sent after the request is ready.
	if strings.Index(out, "to=request_ready") > strings.Index(out, "to=sending_answer") {
		t.Error("stages are logged out of order")
	}
}

// The kid is a hash of this phone's public key and identifies it; a prefix is enough to
// tell sessions apart. The scalar, the seed and the answer are never logged.
func TestSessionLogNeverContainsASecret(t *testing.T) {
	buf := captureLog(t)
	_ = SetLogLevel("trace")
	const phoneSeed = "recognisable-phone-seed-0123456789"
	scalar := runSessionExchange(t, phoneSeed, "secret machine")
	out := buf.String()
	if strings.Contains(out, phoneSeed) {
		t.Error("the identity seed was logged")
	}
	if strings.Contains(out, hex.EncodeToString(scalar)) {
		t.Error("the scalar was logged")
	}
	if strings.Contains(out, "kid=6b") == false && !strings.Contains(out, "kid=") {
		t.Error("no kid prefix was logged")
	}
	// No long hex string anywhere: a full kid, point, scalar or answer would be one.
	if m := regexp.MustCompile(`[0-9a-f]{16,}`).FindString(out); m != "" {
		t.Errorf("a long hex string was logged: %s", m)
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "kid="); i >= 0 {
			rest := line[i+4:]
			if end := strings.IndexAny(rest, " \n"); end >= 0 {
				rest = rest[:end]
			}
			if len(rest) > 8 {
				t.Errorf("more than a kid prefix: %q", rest)
			}
		}
	}
}

// The pairing code is the one thing that lets a stranger enrol: it, the proof, the public
// key and the seed never reach the log, whatever the level.
func TestPairingLogNeverContainsTheCodeOrTheKeys(t *testing.T) {
	buf := captureLog(t)
	_ = SetLogLevel("trace")
	const phoneSeed, machineSeed = "log pair phone seed 9876", "log pair machine seed"
	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	machineID, _ := DeviceIDForSeed(machineSeed)
	addr := freePort(t)
	got := fakeMachine(t, addr, machineSeed, secret, true)
	pub := pairKey()
	if err := Pair(machineID, code, phoneSeed, pub, "my phone", addr, 25); err != nil {
		t.Fatal(err)
	}
	<-got

	out := buf.String()
	if !strings.Contains(out, "pair") || !strings.Contains(out, "to=enrolled") {
		t.Fatalf("nothing about the pairing was logged:\n%s", out)
	}
	for name, secretText := range map[string]string{
		"the pairing code":  code,
		"the secret as hex": hex.EncodeToString(secret),
		"the public key":    pub,
		"the identity seed": phoneSeed,
		"a psk= parameter":  "psk=",
	} {
		if strings.Contains(out, secretText) {
			t.Errorf("%s was logged:\n%s", name, out)
		}
	}
}

// A dial that never gets through is logged with its error text and how many attempts it made,
// at a level that a release build shows.
func TestAFailedConnectIsLoggedWithItsErrorAndAttempts(t *testing.T) {
	buf := captureLog(t) // the default level, warn
	s := NewSession("MACHINE-ID-FOR-A-FAILED-DIAL", "seed for a failed dial log")
	s.SetDirectAddr(freePort(t))
	s.SetTimeoutSeconds(1)
	defer s.Close()
	if err := s.Connect(); err == nil {
		t.Fatal("connected to nothing")
	}
	out := buf.String()
	for _, want := range []string{"level=WARN", "connect failed", "MACHINE-ID-FOR-A-FAILED-DIAL", "attempts=", "error="} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stage") {
		t.Error("trace lines were logged at the default level")
	}
}
