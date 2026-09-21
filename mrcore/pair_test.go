// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"strings"
	"testing"
)

// The proof is what stands between a pairing window and anyone who can
// reach the machine, so it must depend on the secret. Two different
// secrets over the same connection must not produce the same proof.
func TestPairProofDependsOnTheSecret(t *testing.T) {
	const (
		machine = "MACHINE-ID"
		phone   = "PHONE-ID"
	)
	nonce := []byte("a fixed nonce for this test 1234")

	a := PairProof([]byte("secret one"), machine, phone, nonce)
	b := PairProof([]byte("secret two"), machine, phone, nonce)

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
func TestPairProofIsBoundToBothIdentitiesAndTheNonce(t *testing.T) {
	secret := []byte("the pairing secret")
	nonce := []byte("a fixed nonce for this test 1234")
	base := PairProof(secret, "MACHINE-ID", "PHONE-ID", nonce)

	cases := map[string][]byte{
		"a different phone":   PairProof(secret, "MACHINE-ID", "OTHER-PHONE", nonce),
		"a different machine": PairProof(secret, "OTHER-MACHINE", "PHONE-ID", nonce),
		"a different nonce":   PairProof(secret, "MACHINE-ID", "PHONE-ID", []byte("another nonce 56789012345678901")),
	}
	for what, proof := range cases {
		if string(proof) == string(base) {
			t.Errorf("%s produced the same proof", what)
		}
	}
}

// The same inputs must produce the same proof, or the phone and the
// machine can never agree.
func TestPairProofIsDeterministic(t *testing.T) {
	secret := []byte("the pairing secret")
	nonce := []byte("a fixed nonce for this test 1234")
	if string(PairProof(secret, "M", "P", nonce)) != string(PairProof(secret, "M", "P", nonce)) {
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
