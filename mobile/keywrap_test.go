// SPDX-License-Identifier: AGPL-3.0-or-later

package mobile

import (
	"bytes"
	"testing"

	"github.com/muelli/synctang/mrcore"
)

// The long-term key is made here, in Go, and handed to the app once to be
// wrapped by a hardware key. Nothing in the Android Keystore can do the key
// agreement itself on a phone whose Keymaster has none, so the scalar has to
// exist in the app's memory while it is used; these are the calls that make,
// check and use it.

func TestANewKeyScalarIsFreshAndYieldsAPointOnTheCurve(t *testing.T) {
	a, err := NewKeyScalar()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyScalar()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || bytes.Equal(a, b) {
		t.Fatalf("scalars must be 32 bytes and different each time: %x, %x", a, b)
	}
	point, err := PublicPointOf(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(point) != uncompressedPointLen || point[0] != 4 {
		t.Fatalf("not an uncompressed point: %x", point)
	}
	if err := mrcore.ValidateChallengePoint(mrcore.P256(), point); err != nil {
		t.Fatalf("the public point is not on the curve: %v", err)
	}
}

func TestThePublicPointMatchesTheVector(t *testing.T) {
	v := loadVectors(t)
	got, err := PublicPointOf(mustHex(t, v.Recipient.SScalar))
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, v.Recipient.S); !bytes.Equal(got, want) {
		t.Fatalf("\n got %x\nwant %x", got, want)
	}
}

func TestSomethingThatIsNotAScalarIsRefused(t *testing.T) {
	ff := bytes.Repeat([]byte{0xff}, 32) // above the group order
	for name, bad := range map[string][]byte{
		"nothing": nil, "short": make([]byte, 31), "long": make([]byte, 33),
		"zero": make([]byte, 32), "above the order": ff,
	} {
		if _, err := PublicPointOf(bad); err == nil {
			t.Errorf("%s: PublicPointOf accepted it", name)
		}
	}
}

// What the instrumented test on the phone does: enrol against the public point
// of the key that was made, have the scalar answer the challenge, and check the
// machine recovers the secret it enrolled.
func TestTheScalarAnswersAChallengeAndTheMachineRecoversItsSecret(t *testing.T) {
	scalar, err := NewKeyScalar()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := PublicPointOf(scalar)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := EnrolAndChallenge(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ch.FinishWithScalar(scalar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ch.Secret()) {
		t.Fatalf("recovered\n got %x\nwant %x", got, ch.Secret())
	}
}

func TestAnotherScalarDoesNotRecoverTheSecret(t *testing.T) {
	scalar, _ := NewKeyScalar()
	other, _ := NewKeyScalar()
	pub, _ := PublicPointOf(scalar)
	ch, err := EnrolAndChallenge(pub)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ch.FinishWithScalar(other); err == nil && bytes.Equal(got, ch.Secret()) {
		t.Fatal("a different scalar recovered the secret")
	}
}

// The app's copy of the scalar is wiped by the app; this wipes Go's, because a
// slice handed across the boundary is copied and the copy outlives the call
// unless something clears it.
func TestAnsweringWithAScalarWipesTheCopyItWasGiven(t *testing.T) {
	scalar, _ := NewKeyScalar()
	given := append([]byte(nil), scalar...)
	s := NewSession("MACHINE", "seed") // never connected: the call fails, and must still wipe
	if err := s.AnswerWithScalar(given); err == nil {
		t.Fatal("expected an error without a request to answer")
	}
	if !bytes.Equal(given, make([]byte, len(given))) {
		t.Fatalf("the scalar was left in memory: %x", given)
	}
}
