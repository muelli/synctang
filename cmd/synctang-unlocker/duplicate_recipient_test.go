// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
)

// There is one enrolled recipient per transport id. A phone whose app was
// updated made a new key but kept its transport Device ID, so the machine held
// a dead entry (the old key) that matched the phone, and the agent, which took
// the first match, failed every unlock with nothing saying why. These tests
// define what happens instead.

func newPublicKey(t *testing.T) []byte {
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
	return S
}

func kidOf(pub []byte) []byte { k := sha256.Sum256(pub); return k[:] }

func keyslotOfRecipient(t *testing.T, device, id string) string {
	t.Helper()
	tok := exportTokenForTest(t, device)
	for i, rec := range tok.Recipients {
		if got, ok := rec.TransportDeviceID(); ok && canonicalOrRaw(got) == canonicalOrRaw(id) {
			return tok.Keyslots[i]
		}
	}
	t.Fatalf("no recipient for %s", id)
	return ""
}

func tokenBytes(t *testing.T, device string) string {
	t.Helper()
	b, err := json.Marshal(exportTokenForTest(t, device))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func usedSlotCount(t *testing.T, device string) int {
	t.Helper()
	return len(usedKeyslots(luksDumpForTest(t, device)))
}

// ---- enrol -----------------------------------------------------------------

func TestEnrolOnceSameKeyTwiceIsIdempotent(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	pub := newPublicKey(t)

	first, err := enrolOnce(device, pass, pub, "m", "MACHINE-ID", testKH1)
	if err != nil || first.Already {
		t.Fatalf("first enrolment: %+v, %v", first, err)
	}
	slot := keyslotOfRecipient(t, device, testKH1)
	before := tokenBytes(t, device)
	slotsBefore := usedSlotCount(t, device)

	second, err := enrolOnce(device, pass, pub, "m", "MACHINE-ID", testKH1)
	if err != nil {
		t.Fatalf("second enrolment: %v", err)
	}
	if !second.Already || second.Keyslot != slot {
		t.Errorf("second enrolment = %+v, want already enrolled at keyslot %s", second, slot)
	}
	if got := usedSlotCount(t, device); got != slotsBefore {
		t.Errorf("keyslots went from %d to %d", slotsBefore, got)
	}
	if tokenBytes(t, device) != before {
		t.Error("the token changed")
	}
}

func TestEnrolOnceDifferentKeySameIDIsRefusedAndNothingChanges(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	oldPub, newPub := newPublicKey(t), newPublicKey(t)
	if _, err := enrolOnce(device, pass, oldPub, "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	slot := keyslotOfRecipient(t, device, testKH1)
	before := tokenBytes(t, device)
	slotsBefore := usedSlotCount(t, device)

	_, err := enrolOnce(device, pass, newPub, "m", "MACHINE-ID", testKH1)
	if err == nil {
		t.Fatal("a second, different key for the same transport id was enrolled")
	}
	for _, want := range []string{"keyslot " + slot, shortKid(kidOf(oldPub)), "--replace " + testKH1, "--pubkey"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if usedSlotCount(t, device) != slotsBefore || tokenBytes(t, device) != before {
		t.Error("the header changed")
	}
}

func TestEnrolCommandReportsAlreadyEnrolledAndRefusesAConflict(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	dir := t.TempDir()
	passFile := filepath.Join(dir, "pass")
	if err := os.WriteFile(passFile, pass, 0o600); err != nil {
		t.Fatal(err)
	}
	machineKey := filepath.Join(dir, "machine.pem")
	pub, other := newPublicKey(t), newPublicKey(t)
	args := func(key []byte) []string {
		return []string{"enrol", "--device", device, "--pubkey", fmt.Sprintf("%x", key),
			"--recipient-transport-id", testKH1, "--existing-passphrase-file", passFile, "--machine-key-file", machineKey}
	}

	var out, errOut bytes.Buffer
	if code := run(args(pub), &out, &errOut); code != 0 {
		t.Fatalf("first: exit %d, %q", code, errOut.String())
	}
	slot := keyslotOfRecipient(t, device, testKH1)

	out.Reset()
	errOut.Reset()
	if code := run(args(pub), &out, &errOut); code != 0 {
		t.Fatalf("same key again: exit %d, %q", code, errOut.String())
	}
	if want := "already enrolled at keyslot " + slot; !strings.Contains(out.String(), want) {
		t.Errorf("stdout %q should contain %q", out.String(), want)
	}

	out.Reset()
	errOut.Reset()
	if code := run(args(other), &out, &errOut); code == 0 {
		t.Fatalf("a different key for the same id succeeded: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "--replace") {
		t.Errorf("stderr %q should say to use --replace", errOut.String())
	}
}

// ---- remove and replace with duplicates ------------------------------------

// duplicatesImage builds a header with two entries for one transport id, the
// state the bug left behind, by enrolling at the low level, which does not check.
func duplicatesImage(t *testing.T) (device string, pass []byte, oldPub, newPub []byte, slotOld, slotNew string) {
	t.Helper()
	pass = []byte("existing-test-passphrase")
	device = formatLoopbackImage(t, pass)
	oldPub, newPub = newPublicKey(t), newPublicKey(t)
	if err := enrolRecipient(device, pass, oldPub, "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	if err := enrolRecipient(device, pass, newPub, "", "", testKH1); err != nil {
		t.Fatal(err)
	}
	tok := exportTokenForTest(t, device)
	if len(tok.Recipients) != 2 {
		t.Fatalf("want 2 recipients, got %d", len(tok.Recipients))
	}
	return device, pass, oldPub, newPub, tok.Keyslots[0], tok.Keyslots[1]
}

func TestRemoveRefusesWhenSeveralEntriesShareTheID(t *testing.T) {
	device, pass, oldPub, newPub, slotOld, slotNew := duplicatesImage(t)
	before := tokenBytes(t, device)

	err := removeRecipient(device, pass, testKH1, "")
	if err == nil {
		t.Fatal("--remove picked one of two entries")
	}
	for _, want := range []string{slotOld, slotNew, shortKid(kidOf(oldPub)), shortKid(kidOf(newPub)), "--keyslot"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if tokenBytes(t, device) != before {
		t.Error("the token changed")
	}
}

func TestRemoveWithAKeyslotRemovesExactlyThatEntry(t *testing.T) {
	device, pass, _, newPub, slotOld, slotNew := duplicatesImage(t)

	if err := removeRecipient(device, pass, testKH1, slotOld); err != nil {
		t.Fatal(err)
	}
	tok := exportTokenForTest(t, device)
	if len(tok.Recipients) != 1 || tok.Keyslots[0] != slotNew || !bytes.Equal(tok.Recipients[0].Kid, kidOf(newPub)) {
		t.Errorf("token after removal: keyslots %v", tok.Keyslots)
	}
	// A keyslot that does not hold an entry for that id is refused.
	if err := removeRecipient(device, pass, testKH1, "7"); err == nil {
		t.Error("a keyslot that holds no such entry was accepted")
	}
}

// ---- status ----------------------------------------------------------------

func TestStatusWarnsAboutASharedTransportIDAndNamesTheFix(t *testing.T) {
	device, _, oldPub, newPub, slotOld, slotNew := duplicatesImage(t)
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	if _, err := transport.LoadOrCreateCert(machineKey); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %q", code, errOut.String())
	}
	var warning string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "WARNING") {
			warning += line + "\n"
		}
	}
	if warning == "" {
		t.Fatalf("no WARNING line in:\n%s", out.String())
	}
	for _, want := range []string{testKH1, slotOld, slotNew, shortKid(kidOf(oldPub)), shortKid(kidOf(newPub)), "synctang-unlocker enrol", "--remove " + testKH1, "--keyslot"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output should contain %q:\n%s", want, out.String())
		}
	}
}

func TestStatusIsQuietWithoutDuplicates(t *testing.T) {
	pass := []byte("existing-test-passphrase")
	device := formatLoopbackImage(t, pass)
	if err := enrolRecipient(device, pass, newPublicKey(t), "m", "MACHINE-ID", testKH1); err != nil {
		t.Fatal(err)
	}
	machineKey := filepath.Join(t.TempDir(), "machine.pem")
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--device", device, "--machine-key-file", machineKey}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("a warning without duplicates:\n%s", out.String())
	}
}

// ---- agent -----------------------------------------------------------------

type recordingConn struct {
	peer    string
	written bytes.Buffer
}

func (c *recordingConn) PeerID() string              { return c.peer }
func (c *recordingConn) Read(p []byte) (int, error)  { return 0, io.EOF }
func (c *recordingConn) Write(p []byte) (int, error) { return c.written.Write(p) }
func (c *recordingConn) Close() error                { return nil }

func tokenWithRecipients(t *testing.T, ids ...string) mrcore.Token {
	t.Helper()
	tok := mrcore.Token{Version: 1}
	for i, id := range ids {
		rec := recipientWithDevice(t, id)
		rec.Kid = kidOf([]byte{byte(i)})
		tok.Recipients = append(tok.Recipients, rec)
		tok.Keyslots = append(tok.Keyslots, fmt.Sprintf("%d", i+1))
	}
	return tok
}

func TestAgentRefusesBeforeAnythingIsSentWhenSeveralRecipientsMatch(t *testing.T) {
	tok := tokenWithRecipients(t, testKH1, testKH2, testKH1)
	conn := &recordingConn{peer: testKH1}
	err := runExchange(context.Background(), conn, tok, "/dev/null", "m", func([]byte) error { return nil })
	if err == nil {
		t.Fatal("the agent answered for one of two entries")
	}
	for _, want := range []string{"keyslots 1, 3", testKH1, "synctang-unlocker status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
	if conn.written.Len() != 0 {
		t.Errorf("%d bytes were sent to the key holder before refusing", conn.written.Len())
	}
}

func TestMatchRecipientOneAndNone(t *testing.T) {
	tok := tokenWithRecipients(t, testKH1, testKH2)
	rec, err := matchRecipient(tok, testKH2)
	if err != nil || rec == nil || !bytes.Equal(rec.Kid, tok.Recipients[1].Kid) {
		t.Fatalf("one match: %v, %v", rec, err)
	}
	if rec, err := matchRecipient(tok, testKeyholderID); rec != nil || err == nil || !strings.Contains(err.Error(), "does not match any enrolled recipient") {
		t.Fatalf("no match: %v, %v", rec, err)
	}
}

// ---- pair ------------------------------------------------------------------

type pairRun struct {
	result     mrcore.PairResult
	runErr     error
	confirmed  int
	replaceAsk []existingRecipient
	replaceNew [][]byte
	notes      []string
}

// pairWith runs one pairing of f's key holder with the given operator answers.
func pairWith(t *testing.T, f *pairFixture, confirmAnswer, replaceAnswer bool) *pairRun {
	t.Helper()
	secret, _, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	r := &pairRun{}
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		errCh <- runPairOnce(ctx, pairOptions{
			device:             f.device,
			existingPassphrase: f.passphrase,
			machineCert:        f.machineCert,
			machineName:        "test-machine",
			secret:             secret,
			confirm:            func(string, []byte) bool { r.confirmed++; return confirmAnswer },
			confirmReplace: func(_ string, old existingRecipient, newKid []byte) bool {
				r.replaceAsk = append(r.replaceAsk, old)
				r.replaceNew = append(r.replaceNew, newKid)
				return replaceAnswer
			},
			notify:     func(s string) { r.notes = append(r.notes, s) },
			listenOpts: transport.ListenOptions{DirectAddr: f.addr},
		})
	}()
	r.result, err = dialAndPair(t, f, f.addr, secret)
	if err != nil {
		t.Fatalf("key holder side: %v", err)
	}
	r.runErr = <-errCh
	return r
}

func TestPairSameKeyIsAlreadyEnrolledAndAddsNothing(t *testing.T) {
	f := newPairFixture(t)
	if err := enrolRecipient(f.device, f.passphrase, f.holderS, "m", f.machineID, f.holderID); err != nil {
		t.Fatal(err)
	}
	slot := keyslotOfRecipient(t, f.device, f.holderID)
	before, slots := tokenBytes(t, f.device), usedSlotCount(t, f.device)

	r := pairWith(t, f, true, true)
	if !r.result.OK || r.runErr != nil {
		t.Fatalf("result %+v, error %v", r.result, r.runErr)
	}
	if r.confirmed != 0 || len(r.replaceAsk) != 0 {
		t.Errorf("a question was asked about a key that is already enrolled (%d, %d)", r.confirmed, len(r.replaceAsk))
	}
	if len(r.notes) != 1 || !strings.Contains(r.notes[0], "already enrolled at keyslot "+slot) {
		t.Errorf("notes %q", r.notes)
	}
	if tokenBytes(t, f.device) != before || usedSlotCount(t, f.device) != slots {
		t.Error("the header changed")
	}
}

func TestPairDifferentKeyAsksNamingBothAndReplacesOnYes(t *testing.T) {
	f := newPairFixture(t)
	oldPub := newPublicKey(t)
	if err := enrolRecipient(f.device, f.passphrase, oldPub, "m", f.machineID, f.holderID); err != nil {
		t.Fatal(err)
	}
	oldSlot := keyslotOfRecipient(t, f.device, f.holderID)

	r := pairWith(t, f, true, true)
	if !r.result.OK || r.runErr != nil {
		t.Fatalf("result %+v, error %v", r.result, r.runErr)
	}
	if len(r.replaceAsk) != 1 || r.confirmed != 0 {
		t.Fatalf("replace questions %d, plain questions %d", len(r.replaceAsk), r.confirmed)
	}
	if r.replaceAsk[0].Keyslot != oldSlot || !bytes.Equal(r.replaceAsk[0].Kid, kidOf(oldPub)) || !bytes.Equal(r.replaceNew[0], kidOf(f.holderS)) {
		t.Errorf("the question named %+v and %x", r.replaceAsk[0], r.replaceNew[0])
	}
	tok := exportTokenForTest(t, f.device)
	if len(tok.Recipients) != 1 || !bytes.Equal(tok.Recipients[0].Kid, kidOf(f.holderS)) {
		t.Fatalf("after replacing, %d entries", len(tok.Recipients))
	}
	if !opensDeviceForTest(t, f.device, recoverPForTest(t, mrcore.P256(), tok.Recipients[0], f.holderS_)) {
		t.Error("the new key does not open the device")
	}
}

func TestPairDifferentKeyDeclinedChangesNothing(t *testing.T) {
	f := newPairFixture(t)
	if err := enrolRecipient(f.device, f.passphrase, newPublicKey(t), "m", f.machineID, f.holderID); err != nil {
		t.Fatal(err)
	}
	before, slots := tokenBytes(t, f.device), usedSlotCount(t, f.device)

	r := pairWith(t, f, true, false)
	if r.result.OK {
		t.Fatal("pairing succeeded although the operator said no")
	}
	if len(r.notes) == 0 || !strings.Contains(strings.Join(r.notes, "\n"), "nothing was changed") {
		t.Errorf("the operator was not told nothing changed: %q", r.notes)
	}
	if tokenBytes(t, f.device) != before || usedSlotCount(t, f.device) != slots {
		t.Error("the header changed")
	}
}

func TestPairNewTransportIDIsStillAnOrdinaryEnrolment(t *testing.T) {
	f := newPairFixture(t)
	r := pairWith(t, f, true, false)
	if !r.result.OK || r.confirmed != 1 || len(r.replaceAsk) != 0 {
		t.Fatalf("result %+v, plain %d, replace %d", r.result, r.confirmed, len(r.replaceAsk))
	}
}

// The words of the question, and --yes.
func TestPairReplaceConfirmerNamesBothKeysAndHonoursYes(t *testing.T) {
	old := existingRecipient{Keyslot: "3", Kid: kidOf([]byte("old"))}
	newKid := kidOf([]byte("new"))

	var out bytes.Buffer
	ask := pairReplaceConfirmer(false, &out, strings.NewReader("y\n"))
	if !ask(testKH1, old, newKid) {
		t.Error("y was taken as no")
	}
	want := fmt.Sprintf("replace the existing entry for this key holder (keyslot 3, key %s) with this one (key %s)? [y/N] ", shortKid(old.Kid), shortKid(newKid))
	if !strings.Contains(out.String(), want) {
		t.Errorf("prompt %q should contain %q", out.String(), want)
	}

	for _, in := range []string{"n\n", "\n", ""} {
		out.Reset()
		if pairReplaceConfirmer(false, &out, strings.NewReader(in))(testKH1, old, newKid) {
			t.Errorf("%q was taken as yes", in)
		}
	}

	out.Reset()
	if !pairReplaceConfirmer(true, &out, strings.NewReader(""))(testKH1, old, newKid) {
		t.Error("--yes was not yes")
	}
}
