// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"testing"
)

func oneScalar() []byte {
	s := make([]byte, 32)
	s[31] = 1
	return s
}

func twoScalar() []byte {
	s := make([]byte, 32)
	s[31] = 2
	return s
}

// A base point scalar-multiplied by 1 is the generator itself, and by 2 is
// the generator added to itself.
func TestP256ScalarBaseMultAndAdd(t *testing.T) {
	g := P256()

	one, err := g.ScalarBaseMult(oneScalar())
	if err != nil {
		t.Fatalf("ScalarBaseMult(1): %v", err)
	}

	two, err := g.ScalarBaseMult(twoScalar())
	if err != nil {
		t.Fatalf("ScalarBaseMult(2): %v", err)
	}

	sum, err := g.Add(one, two)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	three, err := g.ScalarBaseMult([]byte{
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3,
	})
	if err != nil {
		t.Fatalf("ScalarBaseMult(3): %v", err)
	}

	if !bytes.Equal(sum, three) {
		t.Fatalf("1.G + 2.G != 3.G:\n sum=%x\n three=%x", sum, three)
	}
}

// ECDH is commutative: e.S == s.E for a shared point derived from two
// independent scalar-base-mults. This is the identity the whole MR-1
// recovery step leans on.
func TestP256ScalarMultCommutes(t *testing.T) {
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar s: %v", err)
	}
	e, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar e: %v", err)
	}

	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult s: %v", err)
	}
	E, err := g.ScalarBaseMult(e)
	if err != nil {
		t.Fatalf("ScalarBaseMult e: %v", err)
	}

	sE, err := g.ScalarMult(E, s)
	if err != nil {
		t.Fatalf("ScalarMult(E, s): %v", err)
	}
	eS, err := g.ScalarMult(S, e)
	if err != nil {
		t.Fatalf("ScalarMult(S, e): %v", err)
	}

	if !bytes.Equal(sE, eS) {
		t.Fatalf("s.E != e.S:\n sE=%x\n eS=%x", sE, eS)
	}
}

// Negate followed by Add must cancel out to the point at infinity's x
// coordinate being undefined, so instead check P + (-P) added to a third
// point Q returns Q unchanged.
func TestP256Negate(t *testing.T) {
	g := P256()

	p, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar p: %v", err)
	}
	P, err := g.ScalarBaseMult(p)
	if err != nil {
		t.Fatalf("ScalarBaseMult p: %v", err)
	}

	negP, err := g.Negate(P)
	if err != nil {
		t.Fatalf("Negate: %v", err)
	}

	q, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar q: %v", err)
	}
	Q, err := g.ScalarBaseMult(q)
	if err != nil {
		t.Fatalf("ScalarBaseMult q: %v", err)
	}

	sum, err := g.Add(P, negP)
	if err != nil {
		t.Fatalf("Add(P, negP): %v", err)
	}
	// P + (-P) is the point at infinity, encoded as a single 0x00 byte.
	if !bytes.Equal(sum, []byte{0x00}) {
		t.Fatalf("P + (-P) is not the point at infinity: %x", sum)
	}

	sumQ, err := g.Add(sum, Q)
	if err != nil {
		t.Fatalf("Add(infinity, Q): %v", err)
	}
	if !bytes.Equal(sumQ, Q) {
		t.Fatalf("infinity + Q != Q:\n got=%x\n want=%x", sumQ, Q)
	}
}

// LiftX must return two points sharing the given x-coordinate, one of
// which is the original point that produced that x-coordinate.
func TestP256LiftX(t *testing.T) {
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	P, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	x, err := g.PointToX(P)
	if err != nil {
		t.Fatalf("PointToX: %v", err)
	}

	evenY, oddY, err := g.LiftX(x)
	if err != nil {
		t.Fatalf("LiftX: %v", err)
	}

	if !bytes.Equal(evenY, P) && !bytes.Equal(oddY, P) {
		t.Fatalf("neither LiftX candidate matches the original point:\n P=%x\n evenY=%x\n oddY=%x", P, evenY, oddY)
	}
	if bytes.Equal(evenY, oddY) {
		t.Fatalf("LiftX candidates must differ")
	}

	negP, err := g.Negate(P)
	if err != nil {
		t.Fatalf("Negate: %v", err)
	}
	if !bytes.Equal(evenY, negP) && !bytes.Equal(oddY, negP) {
		t.Fatalf("the other LiftX candidate must be -P:\n negP=%x\n evenY=%x\n oddY=%x", negP, evenY, oddY)
	}
}
