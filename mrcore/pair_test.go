// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A holder nonce for the tests that are about something else.
var testHolderNonce = []byte("a key holder nonce, 32 bytes....")

// The proof is what stands between a pairing window and anyone who can
// reach the machine, so it must depend on the secret. Two different
// secrets over the same connection must not produce the same proof.
func TestPairProofDependsOnTheSecret(t *testing.T) {
	const (
		machine = "MACHINE-ID"
		phone   = "PHONE-ID"
	)
	nonce := []byte("a fixed nonce for this test 1234")
	pub := []byte("a public key")

	a := PairProof([]byte("secret one"), machine, phone, pub, nonce, testHolderNonce)
	b := PairProof([]byte("secret two"), machine, phone, pub, nonce, testHolderNonce)

	if len(a) == 0 {
		t.Fatal("PairProof returned nothing")
	}
	if string(a) == string(b) {
		t.Fatal("two different secrets produced the same proof")
	}
}

// Binding the proof to the connecting device's identity is what stops
// a proof from being useful to anyone but the device that made it. It
// matters because the pairing secret travels by QR code, which can be
// photographed: a proof lifted from one exchange must not authorise a
// different phone.
func TestPairProofIsBoundToBothIdentitiesTheKeyAndTheNonce(t *testing.T) {
	secret := []byte("the pairing secret")
	nonce := []byte("a fixed nonce for this test 1234")
	pub := []byte("the key being enrolled")
	base := PairProof(secret, "MACHINE-ID", "PHONE-ID", pub, nonce, testHolderNonce)

	cases := map[string][]byte{
		"a different phone":        PairProof(secret, "MACHINE-ID", "OTHER-PHONE", pub, nonce, testHolderNonce),
		"a different machine":      PairProof(secret, "OTHER-MACHINE", "PHONE-ID", pub, nonce, testHolderNonce),
		"a different nonce":        PairProof(secret, "MACHINE-ID", "PHONE-ID", pub, []byte("another nonce 56789012345678901"), testHolderNonce),
		"a different public key":   PairProof(secret, "MACHINE-ID", "PHONE-ID", []byte("another key"), nonce, testHolderNonce),
		"a different holder nonce": PairProof(secret, "MACHINE-ID", "PHONE-ID", pub, nonce, []byte("another holder nonce, 32 bytes..")),
	}
	for what, proof := range cases {
		if string(proof) == string(base) {
			t.Errorf("%s produced the same proof", what)
		}
	}
}

// The key being enrolled is part of what the proof vouches for, so it
// must not be possible to move bytes between it and the nonce (or any
// other part) and keep the same proof: each part is length-prefixed.
func TestPairProofKeepsThePublicKeyAndTheNonceApart(t *testing.T) {
	secret := []byte("the pairing secret")
	a := PairProof(secret, "M", "P", []byte("ab"), []byte("c"), testHolderNonce)
	b := PairProof(secret, "M", "P", []byte("a"), []byte("bc"), testHolderNonce)
	if string(a) == string(b) {
		t.Fatal("shifting a byte from the nonce into the public key kept the proof")
	}
}

// The same inputs must produce the same proof, or the phone and the
// machine can never agree.
func TestPairProofIsDeterministic(t *testing.T) {
	secret := []byte("the pairing secret")
	nonce := []byte("a fixed nonce for this test 1234")
	if string(PairProof(secret, "M", "P", []byte("k"), nonce, testHolderNonce)) != string(PairProof(secret, "M", "P", []byte("k"), nonce, testHolderNonce)) {
		t.Fatal("PairProof is not deterministic")
	}
}

// The secret is read off a screen and, when the QR code cannot be
// scanned, typed in by hand, so its encoding has to survive that:
// unambiguous characters, no padding, and case-insensitive on the way
// back in.
func TestPairingSecretEncodingSurvivesBeingTypedIn(t *testing.T) {
	secret, encoded, err := NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	if len(secret) < 16 {
		t.Fatalf("pairing secret is only %d bytes, too few to resist guessing", len(secret))
	}
	if strings.ContainsAny(encoded, "=+/") {
		t.Errorf("encoded secret %q contains characters that are awkward to type or URL-encode", encoded)
	}

	for _, typed := range []string{encoded, strings.ToLower(encoded), strings.ToUpper(encoded)} {
		got, err := DecodePairingSecret(typed)
		if err != nil {
			t.Fatalf("DecodePairingSecret(%q): %v", typed, err)
		}
		if string(got) != string(secret) {
			t.Errorf("DecodePairingSecret(%q) did not round-trip", typed)
		}
	}
}

// Two consecutive pairing windows must not share a secret.
func TestPairingSecretsDiffer(t *testing.T) {
	_, first, err := NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	_, second, err := NewPairingSecret()
	if err != nil {
		t.Fatalf("NewPairingSecret: %v", err)
	}
	if first == second {
		t.Fatal("two pairing secrets came out identical")
	}
}

func TestDecodePairingSecretRejectsRubbish(t *testing.T) {
	for _, bad := range []string{"", "!!!!", "1", "not base32 at all $$"} {
		if _, err := DecodePairingSecret(bad); err == nil {
			t.Errorf("DecodePairingSecret(%q) was accepted", bad)
		}
	}
}

func TestPairSASIsSixDigitsInTwoGroups(t *testing.T) {
	sas := PairSAS("MACHINE", "PEER", []byte{4, 1, 2, 3}, []byte("nonce"), testHolderNonce)
	if len(sas) != 7 || sas[3] != ' ' {
		t.Fatalf("%q, want \"123 456\"", sas)
	}
	for i, c := range sas {
		if i != 3 && (c < '0' || c > '9') {
			t.Fatalf("%q has a character that is not a digit", sas)
		}
	}
}

// Both sides compute it from what they both know, so it has to be a pure function of the four inputs
// and has to change when any one of them does: that is what makes a mismatch mean something.
func TestPairSASDependsOnEveryInput(t *testing.T) {
	machine, peer, pub, nonce := "MACHINE", "PEER", []byte{4, 1, 2, 3}, []byte("nonce-one")
	base := PairSAS(machine, peer, pub, nonce, testHolderNonce)
	if again := PairSAS(machine, peer, pub, nonce, testHolderNonce); again != base {
		t.Fatalf("not deterministic: %q then %q", base, again)
	}
	// Six digits cannot tell every change apart (one in a million would collide), so try several
	// variations of each input and require that most differ.
	changed := func(name string, f func(i int) string) {
		t.Helper()
		same := 0
		const tries = 20
		for i := 0; i < tries; i++ {
			if f(i) == base {
				same++
			}
		}
		if same > 1 {
			t.Errorf("changing the %s left the code the same %d times in %d", name, same, tries)
		}
	}
	changed("machine id", func(i int) string { return PairSAS(machine+string(rune('a'+i)), peer, pub, nonce, testHolderNonce) })
	changed("peer id", func(i int) string { return PairSAS(machine, peer+string(rune('a'+i)), pub, nonce, testHolderNonce) })
	changed("public key", func(i int) string {
		return PairSAS(machine, peer, []byte{4, 1, 2, byte(10 + i)}, nonce, testHolderNonce)
	})
	changed("nonce", func(i int) string { return PairSAS(machine, peer, pub, []byte{byte(i), 9}, testHolderNonce) })
	changed("holder nonce", func(i int) string { return PairSAS(machine, peer, pub, nonce, []byte{byte(i), 7}) })
}

// Moving a byte from one field to the next must not give the same code, which is what length
// prefixes are for.
func TestPairSASFieldsCannotBeShifted(t *testing.T) {
	if PairSAS("AB", "C", []byte{1}, []byte{2}, testHolderNonce) == PairSAS("A", "BC", []byte{1}, []byte{2}, testHolderNonce) {
		t.Error("shifting a byte between the two ids gave the same code")
	}
}

// A fixed value, so that an accidental change to the derivation (which would make a new key holder
// and an old machine disagree) fails a test.
func TestPairSASKnownValue(t *testing.T) {
	got := PairSAS("MACHINE-ID", "PEER-ID", []byte{4, 0xaa, 0xbb}, []byte("a nonce"), testHolderNonce)
	if got != "221 028" {
		t.Fatalf("PairSAS = %q", got)
	}
}

// ---- The key holder commits to a nonce of its own before it sees the machine's. ----
//
// Without that, whoever plays the machine to a key holder chooses the nonce last, after the key holder's key
// and identity are fixed, and can pick one that makes the key holder's short code anything it likes (about a
// million tries, half a second). The attack: pass the real pairing link on with a machine that is the
// attacker's, ask the real machine to enrol the attacker's own key, and make the two codes the operator
// compares agree (a review found and ran this on 2026-11-19). With the commitment, the code also depends on
// a nonce that the key holder reveals only after the machine's nonce is out.

func newTestHolder(t *testing.T, secret []byte) *PairHolder {
	t.Helper()
	h, err := NewPairHolder(secret, "MACHINE-ID", "HOLDER-ID", []byte{4, 1, 2, 3}, "a phone")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPairCommitBindsTheIdentitiesTheKeyAndTheNonceAndKeepsThemApart(t *testing.T) {
	base := PairCommit("M", "P", []byte("key"), testHolderNonce)
	if len(base) != 32 {
		t.Fatalf("commitment is %d bytes", len(base))
	}
	for what, other := range map[string][]byte{
		"machine":     PairCommit("M2", "P", []byte("key"), testHolderNonce),
		"holder":      PairCommit("M", "P2", []byte("key"), testHolderNonce),
		"key":         PairCommit("M", "P", []byte("key2"), testHolderNonce),
		"nonce":       PairCommit("M", "P", []byte("key"), []byte("another holder nonce, 32 bytes..")),
		"shifted ids": PairCommit("MP", "", []byte("key"), testHolderNonce),
		"shifted key": PairCommit("M", "P", []byte("ke"), append([]byte("y"), testHolderNonce...)),
	} {
		if bytes.Equal(other, base) {
			t.Errorf("a different %s gave the same commitment", what)
		}
	}
}

func TestPairRequestTagNeedsTheSecretAndCoversTheCommitment(t *testing.T) {
	a := PairRequestTag([]byte("one"), "M", "P", []byte("key"), []byte("commit"))
	for what, b := range map[string][]byte{
		"secret":     PairRequestTag([]byte("two"), "M", "P", []byte("key"), []byte("commit")),
		"machine":    PairRequestTag([]byte("one"), "M2", "P", []byte("key"), []byte("commit")),
		"holder":     PairRequestTag([]byte("one"), "M", "P2", []byte("key"), []byte("commit")),
		"key":        PairRequestTag([]byte("one"), "M", "P", []byte("key2"), []byte("commit")),
		"commitment": PairRequestTag([]byte("one"), "M", "P", []byte("key"), []byte("commit2")),
	} {
		if bytes.Equal(a, b) {
			t.Errorf("a different %s gave the same tag", what)
		}
	}
}

func TestAKeyHolderAndAMachineThatAgreeShowTheSameCode(t *testing.T) {
	secret := []byte("the pairing secret")
	h := newTestHolder(t, secret)
	req := h.Request()
	if err := CheckPairRequest(secret, "MACHINE-ID", "HOLDER-ID", req); err != nil {
		t.Fatalf("the machine refused an honest request: %v", err)
	}
	challenge := PairChallenge{Nonce: bytes.Repeat([]byte{7}, PairNonceLen)}
	resp, holderCode, err := h.Answer(challenge)
	if err != nil {
		t.Fatal(err)
	}
	machineCode, err := CheckPairResponse(secret, "MACHINE-ID", "HOLDER-ID", req, challenge.Nonce, resp)
	if err != nil {
		t.Fatalf("the machine refused an honest response: %v", err)
	}
	if machineCode != holderCode {
		t.Errorf("the key holder shows %q and the machine %q", holderCode, machineCode)
	}
}

func TestEachConnectionOfAKeyHolderHasANonceOfItsOwn(t *testing.T) {
	secret := []byte("the pairing secret")
	a, b := newTestHolder(t, secret), newTestHolder(t, secret)
	if bytes.Equal(a.Request().Commit, b.Request().Commit) {
		t.Error("two connections made the same commitment")
	}
}

// The request is authenticated before the machine gives out a nonce, so that somebody who does not have the
// pairing secret gets nothing to work with.
func TestAMachineRefusesARequestFromSomebodyWithoutThePairingSecret(t *testing.T) {
	req := newTestHolder(t, []byte("the stranger's guess")).Request()
	if err := CheckPairRequest([]byte("the pairing secret"), "MACHINE-ID", "HOLDER-ID", req); !errors.Is(err, ErrPairRequestTag) {
		t.Errorf("err = %v, want ErrPairRequestTag", err)
	}
	// And one that was made for another connection is not good for this one.
	honest := newTestHolder(t, []byte("the pairing secret")).Request()
	if err := CheckPairRequest([]byte("the pairing secret"), "MACHINE-ID", "OTHER-HOLDER", honest); !errors.Is(err, ErrPairRequestTag) {
		t.Errorf("a request replayed by another device: err = %v", err)
	}
}

// The machine holds the key holder to what it committed to: a nonce that is not the one behind the
// commitment means the key holder looked at the machine's nonce first.
func TestAMachineRefusesAResponseWithANonceThatWasNotCommittedTo(t *testing.T) {
	secret := []byte("the pairing secret")
	h := newTestHolder(t, secret)
	req := h.Request()
	challenge := PairChallenge{Nonce: bytes.Repeat([]byte{7}, PairNonceLen)}
	resp, _, err := h.Answer(challenge)
	if err != nil {
		t.Fatal(err)
	}
	resp.HolderNonce = bytes.Repeat([]byte{1}, PairHolderNonceLen)
	if _, err := CheckPairResponse(secret, "MACHINE-ID", "HOLDER-ID", req, challenge.Nonce, resp); !errors.Is(err, ErrPairCommitment) {
		t.Errorf("err = %v, want ErrPairCommitment", err)
	}
}

func TestAMachineRefusesAResponseWhoseProofIsNotTheOneForThisConnection(t *testing.T) {
	secret := []byte("the pairing secret")
	h := newTestHolder(t, secret)
	req := h.Request()
	challenge := PairChallenge{Nonce: bytes.Repeat([]byte{7}, PairNonceLen)}
	resp, _, _ := h.Answer(challenge)
	resp.Proof = append([]byte(nil), resp.Proof...)
	resp.Proof[0] ^= 1
	if _, err := CheckPairResponse(secret, "MACHINE-ID", "HOLDER-ID", req, challenge.Nonce, resp); !errors.Is(err, ErrPairProof) {
		t.Errorf("err = %v, want ErrPairProof", err)
	}
}

func TestAKeyHolderRefusesANonceThatIsTooShortToBindAnything(t *testing.T) {
	h := newTestHolder(t, []byte("the pairing secret"))
	if _, _, err := h.Answer(PairChallenge{Nonce: make([]byte, PairMinNonceLen-1)}); !errors.Is(err, ErrPairShortNonce) {
		t.Errorf("err = %v, want ErrPairShortNonce", err)
	}
	if _, _, err := h.Answer(PairChallenge{}); !errors.Is(err, ErrPairShortNonce) {
		t.Errorf("no nonce at all: err = %v", err)
	}
}

// A challenge that carries a refusal is what a machine sends instead of a nonce when it will not go on; the
// key holder says that, not that the nonce was short.
func TestAKeyHolderReportsARefusalThatCameInPlaceOfAChallenge(t *testing.T) {
	h := newTestHolder(t, []byte("the pairing secret"))
	_, _, err := h.Answer(PairChallenge{Error: PairRefusedCodeMismatch})
	var refused *PairRefusal
	if !errors.As(err, &refused) || refused.Reason != PairRefusedCodeMismatch {
		t.Errorf("err = %v, want a PairRefusal with the reason", err)
	}
}

// The attack of the review, in memory. The attacker plays the machine to the victim and the key holder to the
// real machine. It sees the victim's request, and the real machine's nonce, and can choose the nonce it sends
// the victim. It fits that nonce to the code the real machine will show, assuming the one thing it does not
// know, the victim's own nonce. The victim's code then does not match, except by luck (one in a million), so
// the attempt is made three times and has to fail every time.
func TestAMachineThatChoosesTheNonceLastCannotFitTheCodeTheKeyHolderShows(t *testing.T) {
	secret := []byte("the pairing secret")
	for attempt := 0; attempt < 3; attempt++ {
		victim, err := NewPairHolder(secret, "ATTACKER-ID", "VICTIM-ID", []byte{4, 9, 9, 9}, "victims phone")
		if err != nil {
			t.Fatal(err)
		}
		victimReq := victim.Request()
		// What the attacker gets from the real machine for its own key: the code the operator will see.
		attacker, _ := NewPairHolder(secret, "MACHINE-ID", "ATTACKER-ID", []byte{4, 1, 1, 1}, victimReq.Name)
		machineNonce := bytes.Repeat([]byte{byte(attempt + 1)}, PairNonceLen)
		_, target, err := attacker.Answer(PairChallenge{Nonce: machineNonce})
		if err != nil {
			t.Fatal(err)
		}

		// Fit a nonce to that target, as well as it can be done without the victim's nonce.
		guess := make([]byte, PairHolderNonceLen)
		fitted := make([]byte, PairNonceLen)
		found := false
		for i := 0; i < 3_000_000 && !found; i++ {
			fitted[0], fitted[1], fitted[2], fitted[3] = byte(i), byte(i>>8), byte(i>>16), byte(attempt)
			if PairSAS("ATTACKER-ID", "VICTIM-ID", []byte{4, 9, 9, 9}, fitted, guess) == target {
				found = true
			}
		}
		if !found {
			t.Fatal("could not fit a nonce even to the guess; the loop is too short")
		}
		_, victimCode, err := victim.Answer(PairChallenge{Nonce: fitted})
		if err != nil {
			t.Fatal(err)
		}
		if victimCode != target {
			return // as it should be
		}
	}
	t.Error("the victim's code matched the target three times: the attacker can fit the code")
}

// The key being enrolled is covered twice. The tag of the request covers it, so a key swapped into the request
// after the key holder made it is refused before any nonce is given out. And the proof covers it, so a proof made
// for one key does not vouch for another.
func TestAKeySwappedIntoARequestAfterItWasMadeIsRefused(t *testing.T) {
	secret := []byte("the pairing secret")
	req := newTestHolder(t, secret).Request()
	req.PubKey = []byte{4, 9, 9, 9}
	if err := CheckPairRequest(secret, "MACHINE-ID", "HOLDER-ID", req); !errors.Is(err, ErrPairRequestTag) {
		t.Errorf("err = %v, want ErrPairRequestTag", err)
	}
}

func TestAProofMadeForAnotherKeyIsRefused(t *testing.T) {
	secret := []byte("the pairing secret")
	h := newTestHolder(t, secret)
	req := h.Request()
	nonce := bytes.Repeat([]byte{7}, PairNonceLen)
	resp := PairResponse{HolderNonce: h.holderNonce, Proof: PairProof(secret, "MACHINE-ID", "HOLDER-ID", []byte{4, 9, 9, 9}, nonce, h.holderNonce)}
	if _, err := CheckPairResponse(secret, "MACHINE-ID", "HOLDER-ID", req, nonce, resp); !errors.Is(err, ErrPairProof) {
		t.Errorf("err = %v, want ErrPairProof", err)
	}
}
