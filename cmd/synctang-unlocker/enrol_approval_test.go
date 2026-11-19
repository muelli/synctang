// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A new key holder can be enrolled with the approval of one that is already enrolled, in place of the passphrase
// of the volume. What its approval recovers is the passphrase of its own keyslot, which is as good as any other
// for adding a keyslot. Nothing here writes a passphrase anywhere: it goes from the exchange into the command.

// newKeyHolder makes a key holder identity: its public key and transport id, as "synctang-keyholder
// export-pubkey" prints them.
func newKeyHolder(t *testing.T) (pubHex, transportID string) {
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
	cert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "holder2.pem"))
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(S), protocol.NewDeviceID(cert.Certificate[0]).String()
}

func (f openFixture) enrolArgs(pubHex, transportID string, extra ...string) []string {
	return append([]string{"enrol", "--device", f.device, "--pubkey", pubHex, "--recipient-transport-id", transportID,
		"--machine-key-file", f.machineKey, "--direct-addr", f.addr}, extra...)
}

func TestEnrolTakesTheApprovalOfAnEnrolledKeyHolderInPlaceOfThePassphrase(t *testing.T) {
	fakeOpener(t) // root, and no mapping in the way
	f := newOpenFixture(t)
	pubHex, id2 := newKeyHolder(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := startOpen(f.enrolArgs(pubHex, id2, "--approve-with-key-holder", "--timeout", "20s"))
	if r := f.answer(t, ctx, f.s); !r.OK {
		t.Fatalf("the machine reported failure: %s", r.Error)
	}
	r := waitFor(t, done)

	if r.code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if plan, err := planEnrolment(f.device, mustHex(t, pubHex), id2); err != nil || plan.Kind != planAlready {
		t.Errorf("the new key holder is not enrolled: %+v %v", plan, err)
	}
	// What was there stays: the passphrase of the volume, and the key holder that approved.
	if err := verifyPassphrase(f.device, []byte("existing-test-passphrase")); err != nil {
		t.Errorf("the original passphrase no longer opens the volume: %v", err)
	}
	if strings.Contains(r.stdout+r.stderr, "existing-test-passphrase") {
		t.Error("the passphrase was printed")
	}
}

func TestEnrolByApprovalEnrolsNobodyWhenNobodyApproves(t *testing.T) {
	fakeOpener(t)
	f := newOpenFixture(t)
	pubHex, id2 := newKeyHolder(t)
	r := waitFor(t, startOpen(f.enrolArgs(pubHex, id2, "--approve-with-key-holder", "--timeout", "1s")))
	if r.code != 1 {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	if plan, _ := planEnrolment(f.device, mustHex(t, pubHex), id2); plan.Kind != planAdd {
		t.Errorf("the new key holder was enrolled without an approval: %+v", plan)
	}
}

func TestEnrolWantsOnePassphraseOrAnApprovalNotBothAndNotNeither(t *testing.T) {
	f := newOpenFixture(t)
	pubHex, id2 := newKeyHolder(t)
	both := waitFor(t, startOpen(f.enrolArgs(pubHex, id2, "--approve-with-key-holder", "--existing-passphrase-file", "/nonexistent")))
	if both.code != 2 || !strings.Contains(both.stderr, "--approve-with-key-holder") {
		t.Errorf("both: exit %d, stderr %q", both.code, both.stderr)
	}
	neither := waitFor(t, startOpen(f.enrolArgs(pubHex, id2)))
	if neither.code != 2 || !strings.Contains(neither.stderr, "--existing-passphrase-file") || !strings.Contains(neither.stderr, "--approve-with-key-holder") {
		t.Errorf("neither: exit %d, stderr %q", neither.code, neither.stderr)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pair takes the same approval, and asks for it before it shows the link, so that the passphrase it stands in for
// is in the command only for the length of the pairing window. The approval is tried through a seam here; the
// exchange itself is what the tests of enrol and extract-keyslot-key above run.
func TestPairTakesTheApprovalInPlaceOfAPassphraseAndAsksForItFirst(t *testing.T) {
	f := newPairFixture(t)
	was := keyFromApprovalFn
	var asked bool
	keyFromApprovalFn = func(device string, a approval, info, stderr io.Writer) ([]byte, int) {
		asked = true
		return append([]byte(nil), f.passphrase...), 0
	}
	t.Cleanup(func() { keyFromApprovalFn = was })

	var out, errOut strings.Builder
	code := run([]string{"pair", "--device", f.device, "--approve-with-key-holder", "--yes",
		"--machine-key-file", filepath.Join(t.TempDir(), "machine.pem"), "--direct-addr", f.addr, "--timeout", "2s", "--qr", "plain"}, &out, &errOut)
	if !asked {
		t.Fatal("no approval was asked for")
	}
	// It got as far as the pairing window: the link is shown, and the window ran out with nobody pairing.
	if !strings.Contains(out.String(), "synctang://pair") || code != 1 || !strings.Contains(errOut.String(), "nobody paired") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
}

func TestPairWantsOnePassphraseOrAnApprovalNotBoth(t *testing.T) {
	var out, errOut strings.Builder
	code := run([]string{"pair", "--device", "/dev/x", "--approve-with-key-holder", "--existing-passphrase-file", "-"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "--approve-with-key-holder") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}
