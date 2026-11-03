// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

type openCall struct{ device, name string }

// fakeOpener stands in for "cryptsetup open", which needs root: it checks that
// the key it is handed really opens the device, the way cryptsetup would, and
// records the call.
func fakeOpener(t *testing.T) (calls func() []openCall) {
	t.Helper()
	var mu sync.Mutex
	var got []openCall
	was, wasRoot, wasExists := openVolume, geteuid, mapperExists
	openVolume = func(device, name string, key []byte) error {
		if err := verifyPassphrase(device, key); err != nil {
			return err
		}
		mu.Lock()
		got = append(got, openCall{device, name})
		mu.Unlock()
		return nil
	}
	geteuid = func() int { return 0 }
	mapperExists = func(string) bool { return false }
	t.Cleanup(func() { openVolume, geteuid, mapperExists = was, wasRoot, wasExists })
	return func() []openCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]openCall(nil), got...)
	}
}

// openFixture is a LUKS image with one enrolled key holder and a machine
// identity, and what a key holder needs to dial it.
type openFixture struct {
	device, machineKey, machineID, addr string
	s                                   []byte
	holderCert                          tlsCert
}

func newOpenFixture(t *testing.T) openFixture {
	t.Helper()
	passphrase := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, passphrase)
	g := mrcore.P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatal(err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatal(err)
	}
	holderCert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "keyholder.pem"))
	if err != nil {
		t.Fatal(err)
	}
	holderID := protocol.NewDeviceID(holderCert.Certificate[0]).String()
	if err := enrolRecipient(device, passphrase, S, "the-machine", "MACHINE-ID", holderID); err != nil {
		t.Fatal(err)
	}
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	machineCert, err := transport.LoadOrCreateCert(machineKey)
	if err != nil {
		t.Fatal(err)
	}
	return openFixture{
		device: device, machineKey: machineKey,
		machineID: protocol.NewDeviceID(machineCert.Certificate[0]).String(),
		addr:      freeTestAddr(t), s: s, holderCert: holderCert,
	}
}

// answer plays the key holder: dial, take the greeting and the request, answer
// with scalar s, and return what the machine says about it.
func (f openFixture) answer(t *testing.T, ctx context.Context, s []byte) mrcore.RecoverResult {
	t.Helper()
	conn, err := transport.NewSyncthingRelay(f.holderCert).Dial(ctx, transport.DialOptions{PeerID: f.machineID, DirectAddr: f.addr})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	var hello mrcore.Hello
	if err := mrcore.ReadMessage(conn, &hello); err != nil {
		t.Fatalf("reading Hello: %v", err)
	}
	if hello.Name != "the-machine" {
		t.Errorf("Hello.Name = %q, want the name given at enrolment", hello.Name)
	}
	var req mrcore.RecoverRequest
	if err := mrcore.ReadMessage(conn, &req); err != nil {
		t.Fatalf("reading RecoverRequest: %v", err)
	}
	Y, err := mrcore.P256().ScalarMult(req.X, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := mrcore.WriteMessage(conn, mrcore.RecoverResponse{Y: Y}); err != nil {
		t.Fatal(err)
	}
	var result mrcore.RecoverResult
	if err := mrcore.ReadMessage(conn, &result); err != nil {
		t.Fatalf("reading RecoverResult: %v", err)
	}
	return result
}

func (f openFixture) args(extra ...string) []string {
	return append([]string{"open", f.device, "--machine-key-file", f.machineKey, "--direct-addr", f.addr}, extra...)
}

type runResult struct {
	code           int
	stdout, stderr string
}

func startOpen(args []string) <-chan runResult {
	done := make(chan runResult, 1)
	go func() {
		var out, errOut bytes.Buffer
		code := run(args, &out, &errOut)
		done <- runResult{code, out.String(), errOut.String()}
	}()
	return done
}

func waitFor(t *testing.T, done <-chan runResult) runResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(30 * time.Second):
		t.Fatal("synctang-unlocker open did not finish")
		return runResult{}
	}
}

// The command exists for a drive plugged in after boot: it waits for a key
// holder, recovers the secret, and opens the volume itself, where the agent
// answers a password request that systemd made.
func TestOpenRecoversTheSecretAndOpensTheVolume(t *testing.T) {
	calls := fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := startOpen(f.args("--timeout", "20s"))
	if r := f.answer(t, ctx, f.s); !r.OK {
		t.Fatalf("the machine reported failure: %s", r.Error)
	}
	r := waitFor(t, done)

	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	got := calls()
	if len(got) != 1 || got[0].device != f.device {
		t.Fatalf("the volume was opened %v, want once on %s", got, f.device)
	}
	if !strings.HasPrefix(got[0].name, "synctang-") {
		t.Errorf("default mapper name %q, want one derived from the volume", got[0].name)
	}
	if !strings.Contains(r.stdout, "/dev/mapper/"+got[0].name) {
		t.Errorf("stdout should say where the volume appeared:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, f.machineID) {
		t.Errorf("stdout should print this machine's transport id so a key holder can dial it:\n%s", r.stdout)
	}
}

func TestOpenUsesTheNameItIsGiven(t *testing.T) {
	calls := fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := startOpen(append(f.args("--timeout", "20s"), "backup"))
	f.answer(t, ctx, f.s)
	r := waitFor(t, done)

	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if got := calls(); len(got) != 1 || got[0].name != "backup" {
		t.Errorf("opened %v, want the name backup", got)
	}
}

// A key holder that answers with the wrong key must not get a volume opened,
// and the command keeps waiting for the right one until its time runs out.
func TestOpenDoesNothingForAnAnswerThatDoesNotOpenTheVolume(t *testing.T) {
	calls := fakeOpener(t)
	f := newOpenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wrong, err := mrcore.P256().RandomScalar()
	if err != nil {
		t.Fatal(err)
	}

	done := startOpen(f.args("--timeout", "6s"))
	if r := f.answer(t, ctx, wrong); r.OK {
		t.Fatal("the machine accepted an answer from the wrong key")
	}
	r := waitFor(t, done)

	if r.code == 0 {
		t.Errorf("exit 0 although nothing was opened; stdout %q", r.stdout)
	}
	if got := calls(); len(got) != 0 {
		t.Errorf("the volume was opened %v for a wrong answer", got)
	}
	if !strings.Contains(r.stderr, "nobody approved") {
		t.Errorf("stderr should say the time ran out, got %q", r.stderr)
	}
}

func TestOpenGivesUpWhenNobodyApproves(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)

	start := time.Now()
	r := waitFor(t, startOpen(f.args("--timeout", "2s")))

	if r.code != 1 || !strings.Contains(r.stderr, "nobody approved within 2s") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %s for a 2s timeout", took)
	}
}

func TestOpenRefusesADeviceThatHasNoKeyHolder(t *testing.T) {
	fakeOpener(t)
	device := formatLoopbackImage(t, []byte("existing-test-passphrase"))

	var out, errOut bytes.Buffer
	code := run([]string{"open", device, "--machine-key-file", filepath.Join(t.TempDir(), "m.pem"), "--timeout", "2s"}, &out, &errOut)

	if code == 0 || !strings.Contains(errOut.String(), "pair") {
		t.Errorf("exit %d, stderr %q: want a refusal that says how to enrol a key holder", code, errOut.String())
	}
}

func TestOpenRefusesANameThatIsAlreadyOpen(t *testing.T) {
	calls := fakeOpener(t)
	mapperExists = func(name string) bool { return name == "backup" }
	f := newOpenFixture(t)

	var out, errOut bytes.Buffer
	code := run(append(f.args("--timeout", "2s"), "backup"), &out, &errOut)

	if code == 0 || !strings.Contains(errOut.String(), "already") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
	if len(calls()) != 0 {
		t.Error("something was opened")
	}
}

func TestOpenRefusesWithoutRootBeforeWaitingForAnyone(t *testing.T) {
	fakeOpener(t)
	geteuid = func() int { return 1000 }
	f := newOpenFixture(t)

	start := time.Now()
	var out, errOut bytes.Buffer
	code := run(f.args("--timeout", "30s"), &out, &errOut)

	if code == 0 || !strings.Contains(errOut.String(), "root") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
	if time.Since(start) > 5*time.Second {
		t.Error("it waited for a key holder before checking it could open anything")
	}
}

func TestOpenRefusesAMapperNameThatIsNotAName(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	for _, bad := range []string{"../x", "a b", "-flag", "", strings.Repeat("a", 80)} {
		var out, errOut bytes.Buffer
		if code := run(append(f.args("--timeout", "2s"), bad), &out, &errOut); code != 2 {
			t.Errorf("name %q: exit %d, want 2 (stderr %q)", bad, code, errOut.String())
		}
	}
}

func TestOpenNeedsADevice(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"open"}, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "open") {
		t.Errorf("stderr should say how to use it: %q", errOut.String())
	}
}

func TestTheDefaultMapperNameComesFromTheVolumesUUID(t *testing.T) {
	got := defaultMapperName(sampleDump)
	if got != "synctang-442828ce" {
		t.Errorf("got %q", got)
	}
	if got := defaultMapperName("no uuid here"); got != "synctang-volume" {
		t.Errorf("without a UUID got %q", got)
	}
}

// tlsCert keeps the fixture's field list short.
type tlsCert = tls.Certificate
