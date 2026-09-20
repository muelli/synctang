// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"crypto/elliptic"
	"math/big"
	"testing"
)

// The key holder computes s.X, where s is its long-term secret and X
// is a point the machine chose. That is a private-key operation on
// attacker-supplied input, and it is the sharpest edge in this design:
// a machine that is compromised, or merely lying, gets to pick the
// point.
//
// The classical attack is to send a point that is not on P-256 at all,
// but lies on some related curve with a smooth group order, and
// recover the scalar from a handful of such queries. The defence is
// simply never to operate on a point that is not on the curve, so
// these check that refusal directly rather than trusting the library
// to be doing it.

// offCurvePoint takes a genuine point and moves it off the curve by
// flipping a bit of the y-coordinate, keeping the encoding otherwise
// well-formed so that only the curve equation can reject it.
func offCurvePoint(t *testing.T) []byte {
	t.Helper()
	g := P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	P, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	bad := append([]byte(nil), P...)
	bad[len(bad)-1] ^= 0x01
	return bad
}

func TestScalarMultRefusesAPointNotOnTheCurve(t *testing.T) {
	g := P256()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}

	if _, err := g.ScalarMult(offCurvePoint(t), s); err == nil {
		t.Fatal("multiplied the long-term secret by a point that is not on the curve")
	}
}

// The same point reaches Add on the machine's side of the exchange.
func TestAddRefusesAPointNotOnTheCurve(t *testing.T) {
	g := P256()
	good, err := g.ScalarBaseMult(mustScalarFor(t, g))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	if _, err := g.Add(good, offCurvePoint(t)); err == nil {
		t.Fatal("added a point that is not on the curve")
	}
	if _, err := g.Add(offCurvePoint(t), good); err == nil {
		t.Fatal("added a point that is not on the curve")
	}
}

// A point whose coordinates are >= the field prime is not a valid
// encoding even if the reduced value would be on the curve; accepting
// it would mean two encodings of one point, which is exactly the kind
// of slack that malleability bugs live in.
func TestScalarMultRefusesOutOfRangeCoordinates(t *testing.T) {
	g := P256()
	p := elliptic.P256().Params().P

	bad := make([]byte, 65)
	bad[0] = 0x04
	// x = p, which is congruent to 0 but not a canonical encoding.
	pb := p.Bytes()
	copy(bad[1+32-len(pb):33], pb)
	copy(bad[33:], new(big.Int).SetInt64(1).Bytes())

	if _, err := g.ScalarMult(bad, mustScalarFor(t, g)); err == nil {
		t.Fatal("accepted a coordinate that is not less than the field prime")
	}
}

// Truncated, empty and over-long encodings must all be refused rather
// than read past or padded out.
func TestScalarMultRefusesMalformedEncodings(t *testing.T) {
	g := P256()
	s := mustScalarFor(t, g)
	good, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	cases := map[string][]byte{
		"empty":          {},
		"one byte":       {0x04},
		"truncated":      good[:len(good)-1],
		"over-long":      append(append([]byte(nil), good...), 0x00),
		"wrong prefix":   append([]byte{0x05}, good[1:]...),
		"all zero bytes": make([]byte, 65),
	}
	for name, point := range cases {
		if _, err := g.ScalarMult(point, s); err == nil {
			t.Errorf("%s: accepted as a point", name)
		}
	}
}

// The scalar side matters too: the machine chooses nothing here, but
// a corrupted key file or a truncated read must not silently become a
// different, weaker secret.
func TestScalarMultRefusesMalformedScalars(t *testing.T) {
	g := P256()
	good, err := g.ScalarBaseMult(mustScalarFor(t, g))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	for name, scalar := range map[string][]byte{
		"empty":     {},
		"too short": make([]byte, 31),
		"too long":  make([]byte, 33),
	} {
		if _, err := g.ScalarMult(good, scalar); err == nil {
			t.Errorf("%s: accepted as a scalar", name)
		}
	}
}

// LiftX is fed an x-coordinate straight from a hardware key holder's
// ECDH result, so it is attacker-supplied whenever the key holder is.
func TestLiftXRefusesCoordinatesWithNoPoint(t *testing.T) {
	g := P256()
	// Roughly half of all field elements are not the x-coordinate of
	// any point, so a short search finds one.
	var found bool
	for i := 0; i < 64 && !found; i++ {
		x := make([]byte, 32)
		x[31] = byte(i)
		if _, _, err := g.LiftX(x); err != nil {
			found = true
		}
	}
	if !found {
		t.Skip("no non-residue found in the search range; not a failure of the code")
	}

	for name, x := range map[string][]byte{
		"empty":     {},
		"too short": make([]byte, 31),
		"too long":  make([]byte, 33),
	} {
		if _, _, err := g.LiftX(x); err == nil {
			t.Errorf("%s: accepted as an x-coordinate", name)
		}
	}
}

// The group itself accepts the point at infinity, and should: s.O = O
// is well defined, and the answer is independent of s, so nothing
// leaks. It is the protocol layer that refuses to operate on it, since
// a machine has no legitimate reason to send one and a key holder that
// quietly obliges hides the fact that it is being probed.
func TestTheGroupAcceptsInfinityButTheProtocolRefusesIt(t *testing.T) {
	g := P256()
	infinity := []byte{0x00}

	if _, err := g.ScalarMult(infinity, mustScalarFor(t, g)); err != nil {
		t.Fatalf("the group should treat infinity as a point: %v", err)
	}
	if err := ValidateChallengePoint(g, infinity); err == nil {
		t.Fatal("a key holder must refuse a challenge that is the point at infinity")
	}
}

// Everything a hostile machine could put in the challenge field has to
// be refused before the long-term secret is used.
func TestValidateChallengePointRefusesHostileInput(t *testing.T) {
	g := P256()
	good, err := g.ScalarBaseMult(mustScalarFor(t, g))
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	if err := ValidateChallengePoint(g, good); err != nil {
		t.Fatalf("a genuine challenge point was refused: %v", err)
	}

	for name, x := range map[string][]byte{
		"empty":        {},
		"infinity":     {0x00},
		"off curve":    offCurvePoint(t),
		"truncated":    good[:len(good)-1],
		"over-long":    append(append([]byte(nil), good...), 0x00),
		"wrong prefix": append([]byte{0x05}, good[1:]...),
		"all zeroes":   make([]byte, 65),
	} {
		if err := ValidateChallengePoint(g, x); err == nil {
			t.Errorf("%s: accepted as a challenge point", name)
		}
	}
}

func mustScalarFor(t *testing.T, g Group) []byte {
	t.Helper()
	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	return s
}

var _ = bytes.Equal

// A peer that declares an enormous message must not make the reader
// allocate for it. The length prefix is attacker-controlled and is
// read before any of the body, so believing it is how a four-byte
// write becomes a four-gigabyte allocation.
func TestReadMessageRefusesAnEnormousDeclaredLength(t *testing.T) {
	for name, size := range map[string]uint32{
		"just over the limit": maxMessageSize + 1,
		"a megabyte":          1 << 20,
		"four gigabytes":      0xFFFFFFFF,
	} {
		var buf bytes.Buffer
		var lenBuf [4]byte
		lenBuf[0] = byte(size >> 24)
		lenBuf[1] = byte(size >> 16)
		lenBuf[2] = byte(size >> 8)
		lenBuf[3] = byte(size)
		buf.Write(lenBuf[:])
		// Deliberately send almost nothing after the prefix: a reader
		// that trusted the length would sit waiting for bytes that are
		// never coming, holding the buffer it already allocated.
		buf.WriteString("{}")

		var hello Hello
		if err := ReadMessage(&buf, &hello); err == nil {
			t.Errorf("%s: accepted a declared length of %d bytes", name, size)
		}
	}
}
