// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muelli/synctang/mrcore"
	"github.com/muelli/synctang/mrcore/transport"
	"github.com/syncthing/syncthing/lib/protocol"
)

// fakeMachine plays the machine's half of a pairing the way "synctang-unlocker pair" does (this
// package does not import that one): it sends a challenge, checks the proof of the code, works out the
// short code it would show the operator, and answers with the verdict it is told to.
type fakeMachine struct {
	id     string
	addr   string
	secret []byte
	code   string // the pairing code as the link carries it

	// verdict decides the answer once the proof has checked out.
	verdict func() mrcore.PairResult

	// nonce is what the machine sends (a fixed one when nil).
	nonce []byte

	got chan fakePairing
}

type fakePairing struct {
	peerID string
	pubKey []byte
	name   string
	sas    string
	proof  bool
	err    error
}

func newFakeMachine(t *testing.T, verdict func() mrcore.PairResult) *fakeMachine {
	t.Helper()
	cert, err := transport.LoadOrCreateCert(filepath.Join(t.TempDir(), "machine.pem"))
	if err != nil {
		t.Fatal(err)
	}
	secret, code, err := mrcore.NewPairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeMachine{
		id:      protocol.NewDeviceID(cert.Certificate[0]).String(),
		addr:    freeTestAddr(t),
		secret:  secret,
		code:    code,
		verdict: verdict,
		got:     make(chan fakePairing, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	go func() {
		conn, err := transport.NewSyncthingRelay(cert).Listen(ctx, transport.ListenOptions{DirectAddr: m.addr})
		if err != nil {
			m.got <- fakePairing{err: err}
			return
		}
		defer conn.Close()
		// The key holder speaks first; the nonce is chosen after the key is known.
		var req mrcore.PairRequest
		if err := mrcore.ReadMessage(conn, &req); err != nil {
			m.got <- fakePairing{err: err}
			return
		}
		nonce := m.nonce
		if nonce == nil {
			nonce = []byte("a-nonce-for-this-connection-0123")
		}
		if err := mrcore.WriteMessage(conn, mrcore.PairChallenge{Nonce: nonce, Name: "fake-machine"}); err != nil {
			m.got <- fakePairing{err: err}
			return
		}
		var resp mrcore.PairResponse
		if err := mrcore.ReadMessage(conn, &resp); err != nil {
			m.got <- fakePairing{err: err}
			return
		}
		p := fakePairing{peerID: conn.PeerID(), pubKey: req.PubKey, name: req.Name}
		p.proof = string(mrcore.PairProof(secret, m.id, p.peerID, req.PubKey, nonce)) == string(resp.Proof)
		p.sas = mrcore.PairSAS(m.id, p.peerID, req.PubKey, nonce)
		result := mrcore.PairResult{Error: mrcore.PairRefusedCodeMismatch}
		if p.proof {
			result = m.verdict()
		}
		p.err = mrcore.WriteMessage(conn, result)
		m.got <- p
		time.Sleep(200 * time.Millisecond) // let the verdict be read before the connection closes
	}()
	return m
}

func (m *fakeMachine) link(name string) string {
	q := url.Values{}
	q.Set("machine", m.id)
	if name != "" {
		q.Set("name", name)
	}
	q.Set("psk", m.code)
	return "synctang://pair?" + q.Encode()
}

func (m *fakeMachine) result(t *testing.T) fakePairing {
	t.Helper()
	select {
	case p := <-m.got:
		if p.err != nil {
			t.Fatalf("fake machine: %v", p.err)
		}
		return p
	case <-time.After(20 * time.Second):
		t.Fatal("the fake machine was never reached")
		return fakePairing{}
	}
}

func accept() mrcore.PairResult { return mrcore.PairResult{OK: true} }

func pairArgs(m *fakeMachine, book, keyFile, transportFile string, extra ...string) []string {
	args := []string{"pair", "--direct-addr", m.addr, "--timeout", "10s", "--machines-file", book,
		"--key-file", keyFile, "--transport-key-file", transportFile}
	return append(args, extra...)
}

func TestPairEnrolsTheKeyHolderShowsTheSameCodeAndNamesTheMachine(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)

	code, out, errOut := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file", "--name", "my-laptop"), m.link("study-box"))...)
	if code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	got := m.result(t)
	if !got.proof {
		t.Fatal("the machine did not accept the proof of the code")
	}
	if got.name != "my-laptop" {
		t.Errorf("the machine was told the name %q", got.name)
	}
	if !strings.Contains(out, got.sas) {
		t.Errorf("the code the machine shows is %q; the key holder printed:\n%s", got.sas, out)
	}
	// With no key yet, pairing made one, and what the machine enrolled is that key.
	_, exported, _ := runKH("export-pubkey", "--key-file", keyFile, "--transport-key-file", transportFile)
	if !strings.Contains(exported, "S: "+hex.EncodeToString(got.pubKey)+"\n") {
		t.Errorf("the machine was sent a key that is not this key holder's:\n%s", exported)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("no key was made: %v", err)
	}
	// The name in the link is what the machine can now be unlocked by.
	ms, err := loadMachines(book)
	if err != nil || len(ms) != 1 || ms[0].Name != "study-box" || ms[0].ID != m.id {
		t.Fatalf("machines = %+v, %v", ms, err)
	}
	if !strings.Contains(out, "synctang-keyholder unlock study-box") {
		t.Errorf("it does not say how to unlock now:\n%s", out)
	}
}

func TestPairUsesTheKeyThereAlreadyIsAndDoesNotReplaceIt(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	if code, _, e := runKH("init", "--store", "file", "--key-file", keyFile, "--transport-key-file", transportFile); code != 0 {
		t.Fatal(e)
	}
	before := pubOf(t, keyFile, transportFile)
	if code, out, e := runKH(append(pairArgs(m, booksPath(t), keyFile, transportFile), m.link("box"))...); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, e)
	}
	got := m.result(t)
	if "S: "+hex.EncodeToString(got.pubKey) != before {
		t.Errorf("the machine was sent %x, not the existing key %s", got.pubKey, before)
	}
	if pubOf(t, keyFile, transportFile) != before {
		t.Error("pairing changed the key")
	}
}

func TestPairWithAWrongCodeSaysSoAndSavesNothing(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	wrong := strings.Replace(m.link("box"), m.code, strings.Repeat("A", len(m.code)), 1)
	code, out, errOut := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file"), wrong)...)
	if code != 1 || !strings.Contains(errOut, "code") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(book); !os.IsNotExist(err) {
		t.Error("a name was saved for a machine that refused")
	}
}

func TestPairDeclinedByTheOperatorSaysSoAndSavesNothing(t *testing.T) {
	m := newFakeMachine(t, func() mrcore.PairResult { return mrcore.PairResult{Error: mrcore.PairRefusedDeclined} })
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	code, out, errOut := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file"), m.link("box"))...)
	if code != 1 || !strings.Contains(errOut, "declined") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	// The code was still shown: whoever is at the laptop needs it to know why the operator said no.
	if got := m.result(t); !strings.Contains(out, got.sas) {
		t.Errorf("the code was not printed:\n%s", out)
	}
	if _, err := os.Stat(book); !os.IsNotExist(err) {
		t.Error("a name was saved for a machine that declined")
	}
}

func TestPairNameFromTheCommandLineWinsAndNoNameMeansNoAlias(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	if code, out, e := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file", "--as", "mine"), m.link("theirs"))...); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, e)
	}
	if ms, _ := loadMachines(book); len(ms) != 1 || ms[0].Name != "mine" {
		t.Errorf("--as should name it: %+v", ms)
	}

	m2 := newFakeMachine(t, accept)
	book2 := booksPath(t)
	code, out, e := runKH(append(pairArgs(m2, book2, keyFile, transportFile), m2.link(""))...)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, e)
	}
	if _, err := os.Stat(book2); !os.IsNotExist(err) {
		t.Error("a name was invented for a machine that has none")
	}
	if !strings.Contains(out, "synctang-keyholder unlock "+m2.id) || !strings.Contains(out, "machines add") {
		t.Errorf("without a name it should give the id to unlock with, and say how to name it:\n%s", out)
	}
}

func TestPairTakesOverANameThatMeantAnotherMachineWithForceAndSaysSo(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "box", machineIDB)
	code, out, e := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file", "--force"), m.link("box"))...)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, e)
	}
	if ms, _ := loadMachines(book); len(ms) != 1 || ms[0].ID != m.id {
		t.Errorf("the name should mean the machine just paired with: %+v", ms)
	}
	if !strings.Contains(out+e, machineIDB) {
		t.Errorf("the machine the name used to mean should be named:\n%s%s", out, e)
	}
}

func TestPairRefusesWhatIsNotAPairingLink(t *testing.T) {
	keyFile, transportFile := keyholderDir(t)
	for _, link := range []string{
		"", "https://example.com/", "synctang://other?machine=X", "synctang://pair?psk=AAAA",
		"synctang://pair?machine=" + machineIDA, "synctang://pair?machine=not-an-id&psk=AAAA",
	} {
		args := []string{"pair", "--machines-file", booksPath(t), "--key-file", keyFile, "--transport-key-file", transportFile, "--store", "file"}
		if link != "" {
			args = append(args, link)
		}
		if code, _, e := runKH(args...); code != 2 || !strings.Contains(e, "pairing link") {
			t.Errorf("%q: exit %d: %s", link, code, e)
		}
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("a key was made for a link that was refused")
	}
}

// --- what a review of the pairing code found ---

func TestPairRefusesANonceTooShortToBindAnythingTo(t *testing.T) {
	m := newFakeMachine(t, accept)
	m.nonce = []byte("1234")
	keyFile, transportFile := keyholderDir(t)
	code, _, errOut := runKH(append(pairArgs(m, booksPath(t), keyFile, transportFile, "--store", "file"), m.link("box"))...)
	if code != 1 || !strings.Contains(errOut, "too short") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestWhatTheMachineSaysWhenItRefusesCannotActOnTheTerminal(t *testing.T) {
	m := newFakeMachine(t, func() mrcore.PairResult {
		return mrcore.PairResult{Error: "nope\x1b]0;owned\x07\nsynctang-keyholder: forged line"}
	})
	keyFile, transportFile := keyholderDir(t)
	code, out, errOut := runKH(append(pairArgs(m, booksPath(t), keyFile, transportFile, "--store", "file"), m.link("box"))...)
	if code != 1 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if strings.ContainsAny(errOut, "\x1b\x07") || strings.Contains(errOut, "\nsynctang-keyholder: forged line") {
		t.Errorf("the machine's words reached the terminal as they were:\n%q", errOut)
	}
}

// A link is whoever made it: a name in it that would clear the screen is shown as what it is, and a name that
// cannot be a name here does not stop the pairing, it only means there is no name to save.
func TestAHostileOrUnusableNameInTheLinkIsShownSafelyAndDoesNotStopPairing(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	code, out, errOut := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file"), m.link("my\x1b[2Jmachine with spaces"))...)
	if code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	if strings.ContainsAny(out+errOut, "\x1b") {
		t.Errorf("an escape reached the terminal:\n%q", out+errOut)
	}
	if _, err := os.Stat(book); !os.IsNotExist(err) {
		t.Error("a name that is not a usable name was saved")
	}
	if !strings.Contains(out, "synctang-keyholder unlock "+m.id) || !strings.Contains(out, "--as") {
		t.Errorf("without a name it should give the id to unlock with, and say how to name it:\n%s", out)
	}
}

// A link is somebody's word, and a name in it is theirs: it does not quietly take over a name the person already
// uses for another machine.
func TestAPairingDoesNotTakeOverANameForAnotherMachineWithoutForce(t *testing.T) {
	m := newFakeMachine(t, accept)
	keyFile, transportFile := keyholderDir(t)
	book := booksPath(t)
	_, _, _ = runKH("machines", "add", "--machines-file", book, "my-server", machineIDB)
	code, _, errOut := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file"), m.link("my-server"))...)
	if code != 1 || !strings.Contains(errOut, machineIDB) || !strings.Contains(errOut, "--force") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if ms, _ := loadMachines(book); len(ms) != 1 || ms[0].ID != machineIDB {
		t.Errorf("the name was changed: %+v", ms)
	}
	// Nothing was sent anywhere before that was said.
	select {
	case p := <-m.got:
		t.Errorf("the machine was dialled: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
	code, out, e := runKH(append(pairArgs(m, book, keyFile, transportFile, "--store", "file", "--force"), m.link("my-server"))...)
	if code != 0 || !strings.Contains(out+e, machineIDB) {
		t.Fatalf("with --force: exit %d: %s%s", code, out, e)
	}
	if ms, _ := loadMachines(book); len(ms) != 1 || ms[0].ID != m.id {
		t.Errorf("--force did not take the name: %+v", ms)
	}
}

func TestALinkWithASettingThisProgramDoesNotKnowOrOneTwiceIsRefused(t *testing.T) {
	m := newFakeMachine(t, accept)
	good := m.link("box")
	for name, link := range map[string]string{
		"an unknown setting": good + "&relay=relay%3A%2F%2Fevil.example",
		"a name twice":       good + "&name=other",
		"a machine twice":    good + "&machine=" + machineIDB,
		"a code twice":       good + "&psk=" + m.code,
	} {
		if _, err := parsePairingLink(link); err == nil || !strings.Contains(err.Error(), "pairing link") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := parsePairingLink(good); err != nil {
		t.Errorf("a good link: %v", err)
	}
}

// A discovery server in a link that has a space in it would be written into the address book as a line of four
// fields, and the book would then not read at all.
func TestADiscoveryServerWithWhitespaceIsRefusedBeforeItIsSavedAnywhere(t *testing.T) {
	m := newFakeMachine(t, accept)
	link := m.link("box") + "&discovery=" + url.QueryEscape("https://disco.example/a b?id="+machineIDA)
	if _, err := parsePairingLink(link); err == nil || !strings.Contains(err.Error(), "discovery") {
		t.Errorf("%v", err)
	}
	if _, _, err := putMachine(booksPath(t), machine{Name: "box", ID: machineIDA, Discovery: "https://d.example/a b"}, true); err == nil {
		t.Error("a discovery URL with a space was saved")
	}
}
