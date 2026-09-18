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
	buf := make([]byte, 32)
	for {
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("mrcore: random scalar: %w", err)
		}
		if !isAllZero(buf) {
			return buf, nil
		}
	}
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
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

func (p256Group) Negate(point []byte) ([]byte, error) {
	if len(point) == p256InfinityEncoded && point[0] == 0 {
		return point, nil // -infinity is infinity
	}
	if len(point) != p256PointLen || point[0] != 0x04 {
		return nil, errors.New("mrcore: negate requires an uncompressed point")
	}

	fieldPrime := elliptic.P256().Params().P
	y := new(big.Int).SetBytes(point[1+p256CoordLen:])
	y.Sub(fieldPrime, y)
	y.Mod(y, fieldPrime)

	out := make([]byte, p256PointLen)
	out[0] = 0x04
	copy(out[1:1+p256CoordLen], point[1:1+p256CoordLen])
	yBytes := y.Bytes()
	copy(out[p256PointLen-len(yBytes):], yBytes)

	if _, err := nistec.NewP256Point().SetBytes(out); err != nil {
		return nil, fmt.Errorf("mrcore: negated point is not on the curve: %w", err)
	}
	return out, nil
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
