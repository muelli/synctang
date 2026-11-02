// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mrcore implements the MR-1 protocol: enrolment and recovery of
// a LUKS volume secret against one or more key holders, without either
// side ever learning the other's long-term secret. See the design plan
// for the full protocol description and threat model.
package mrcore

import (
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"

	"filippo.io/nistec"
)

// Group abstracts the elliptic curve group MR-1 runs over, so a future
// software-only recipient type (for example ristretto255) could
// implement this interface without changing the enrolment or recovery
// code in mr1.go. Only a P-256 implementation exists today: it was
// chosen because Android Keystore and TPM2 support ECDH on it, which
// neither supports for Curve25519.
//
// Points and scalars are opaque byte slices, in the same uncompressed
// SEC1 point encoding and big-endian scalar encoding nistec uses, so
// implementations can be swapped without changing callers.
type Group interface {
	// RandomScalar returns a uniformly random non-zero scalar.
	RandomScalar() ([]byte, error)
	// ScalarBaseMult returns scalar.G.
	ScalarBaseMult(scalar []byte) ([]byte, error)
	// ScalarMult returns scalar.point.
	ScalarMult(point, scalar []byte) ([]byte, error)
	// Add returns a + b.
	Add(a, b []byte) ([]byte, error)
	// Negate returns -point.
	Negate(point []byte) ([]byte, error)
	// PointToX returns the x-coordinate encoding of point.
	PointToX(point []byte) ([]byte, error)
	// LiftX returns the two points on the curve with the given
	// x-coordinate. Used to recover a point from an x-only ECDH result,
	// such as Android Keystore or TPM2 return.
	LiftX(x []byte) (evenY, oddY []byte, err error)
}

// P256 returns the NIST P-256 implementation of Group.
func P256() Group { return p256Group{} }

type p256Group struct{}

func (p256Group) RandomScalar() ([]byte, error) {
	return randomScalarFrom(rand.Reader)
}

// randomScalarFrom draws a scalar uniformly from [1, n-1] by rejection
// sampling: 32 bytes at a time, discarding any that are zero or not less
// than the group order n. Reducing a draw modulo n instead would be
// biased, and accepting anything non-zero would let values at or above n
// through.
func randomScalarFrom(r io.Reader) ([]byte, error) {
	n := elliptic.P256().Params().N
	buf := make([]byte, 32)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("mrcore: random scalar: %w", err)
		}
		v := new(big.Int).SetBytes(buf)
		if v.Sign() > 0 && v.Cmp(n) < 0 {
			return buf, nil
		}
	}
}

// ValidatePrivateScalar checks that scalar is a usable P-256 private key:
// 32 bytes, and as a big-endian number in [1, n-1]. Zero would give the point
// at infinity and anything from n up is a different spelling of a smaller
// scalar, so neither may become a key.
func ValidatePrivateScalar(scalar []byte) error {
	if len(scalar) != 32 {
		return fmt.Errorf("mrcore: a P-256 scalar is 32 bytes, got %d", len(scalar))
	}
	// Compared without branching on the value and without copying it: the
	// scalar is a secret, and both a big.Int and crypto/ecdh's NewPrivateKey
	// (which clones its input into a key object nothing wipes) leave a copy
	// on the heap.
	if !scalarInRange(scalar, p256Order) {
		return fmt.Errorf("mrcore: scalar is not in [1, n-1]")
	}
	return nil
}

// p256Order is n, the order of the group, big-endian. Public.
var p256Order = elliptic.P256().Params().N.FillBytes(make([]byte, 32))

// scalarInRange reports whether s, big-endian, is in [1, n-1], without
// branching on s and without copying it. The subtraction s - n is done byte by
// byte from the least significant end; a borrow out of the top means s < n.
// The final test branches on two bits derived from s, which is the answer
// itself and not the value.
func scalarInRange(s, n []byte) bool {
	if len(s) != len(n) {
		return false
	}
	var nonzero byte
	var borrow uint16
	for i := len(s) - 1; i >= 0; i-- {
		nonzero |= s[i]
		d := uint16(s[i]) - uint16(n[i]) - borrow
		borrow = (d >> 15) & 1
	}
	return nonzero != 0 && borrow == 1
}

func (p256Group) ScalarBaseMult(scalar []byte) ([]byte, error) {
	p, err := nistec.NewP256Point().ScalarBaseMult(scalar)
	if err != nil {
		return nil, fmt.Errorf("mrcore: scalar base mult: %w", err)
	}
	return p.Bytes(), nil
}

func (p256Group) ScalarMult(point, scalar []byte) ([]byte, error) {
	q, err := nistec.NewP256Point().SetBytes(point)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid point: %w", err)
	}
	r, err := nistec.NewP256Point().ScalarMult(q, scalar)
	if err != nil {
		return nil, fmt.Errorf("mrcore: scalar mult: %w", err)
	}
	return r.Bytes(), nil
}

func (p256Group) Add(a, b []byte) ([]byte, error) {
	pa, err := nistec.NewP256Point().SetBytes(a)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid point a: %w", err)
	}
	pb, err := nistec.NewP256Point().SetBytes(b)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid point b: %w", err)
	}
	r := nistec.NewP256Point().Add(pa, pb)
	return r.Bytes(), nil
}

const (
	p256PointLen        = 65 // uncompressed: 0x04 || X || Y
	p256CoordLen        = 32
	p256InfinityEncoded = 1 // point at infinity: a single 0x00 byte
)

// negateScalar is the group order minus one. Multiplying by it is
// negation, since (n-1).P = n.P - P = -P for a point of order n.
var negateScalar = negateScalarBytes()

func negateScalarBytes() []byte {
	n := new(big.Int).Sub(elliptic.P256().Params().N, big.NewInt(1))
	out := make([]byte, p256CoordLen)
	nb := n.Bytes()
	copy(out[p256CoordLen-len(nb):], nb)
	return out
}

// Negate returns -point.
//
// Implemented as multiplication by the group order minus one rather
// than by subtracting the y-coordinate from the field prime. The
// arithmetic is the same either way, but this hands it to nistec's
// constant-time scalar multiplication instead of doing modular
// arithmetic here with big.Int, which is variable-time and was the
// only hand-rolled field arithmetic left in this package. Pinned
// against the (x, p-y) definition by TestP256NegateMatchesTheField-
// Definition, which computes the expected answer independently.
func (p256Group) Negate(point []byte) ([]byte, error) {
	if len(point) == p256InfinityEncoded && point[0] == 0 {
		return point, nil // -infinity is infinity
	}
	if len(point) != p256PointLen || point[0] != 0x04 {
		return nil, errors.New("mrcore: negate requires an uncompressed point")
	}

	p, err := nistec.NewP256Point().SetBytes(point)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid point: %w", err)
	}
	r, err := nistec.NewP256Point().ScalarMult(p, negateScalar)
	if err != nil {
		return nil, fmt.Errorf("mrcore: negate: %w", err)
	}
	return r.Bytes(), nil
}

func (p256Group) PointToX(point []byte) ([]byte, error) {
	p, err := nistec.NewP256Point().SetBytes(point)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid point: %w", err)
	}
	x, err := p.BytesX()
	if err != nil {
		return nil, fmt.Errorf("mrcore: point to x: %w", err)
	}
	return x, nil
}

func (p256Group) LiftX(x []byte) (evenY, oddY []byte, err error) {
	if len(x) != p256CoordLen {
		return nil, nil, fmt.Errorf("mrcore: x must be %d bytes, got %d", p256CoordLen, len(x))
	}

	compressedEven := append([]byte{0x02}, x...)
	compressedOdd := append([]byte{0x03}, x...)

	pe, err := nistec.NewP256Point().SetBytes(compressedEven)
	if err != nil {
		return nil, nil, fmt.Errorf("mrcore: x is not a valid P-256 x-coordinate: %w", err)
	}
	po, err := nistec.NewP256Point().SetBytes(compressedOdd)
	if err != nil {
		return nil, nil, fmt.Errorf("mrcore: x is not a valid P-256 x-coordinate: %w", err)
	}
	return pe.Bytes(), po.Bytes(), nil
}
