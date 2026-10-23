// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"crypto/elliptic"
	"io"
	"math/big"
	"testing"
)

func scalarBytes(n *big.Int) []byte {
	out := make([]byte, 32)
	n.FillBytes(out)
	return out
}

// A uniform scalar in [1, n-1] needs rejection sampling: reducing a
// 256-bit draw modulo n, or accepting anything non-zero, is biased and
// lets values at or above n through.
func TestRandomScalarRejectsValuesOutsideOneToNMinusOne(t *testing.T) {
	n := elliptic.P256().Params().N
	valid := scalarBytes(big.NewInt(7))

	var feed bytes.Buffer
	feed.Write(scalarBytes(n))                                  // n itself
	feed.Write(scalarBytes(new(big.Int).Add(n, big.NewInt(1)))) // n + 1
	feed.Write(bytes.Repeat([]byte{0xff}, 32))                  // 2^256 - 1
	feed.Write(make([]byte, 32))                                // zero
	feed.Write(valid)

	got, err := randomScalarFrom(&feed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, valid) {
		t.Fatalf("randomScalarFrom = %x, want %x (the first value in [1, n-1])", got, valid)
	}
}

func TestRandomScalarAcceptsTheBoundaries(t *testing.T) {
	n := elliptic.P256().Params().N
	for name, v := range map[string]*big.Int{
		"one":       big.NewInt(1),
		"n minus 1": new(big.Int).Sub(n, big.NewInt(1)),
	} {
		got, err := randomScalarFrom(bytes.NewReader(scalarBytes(v)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if new(big.Int).SetBytes(got).Cmp(v) != 0 {
			t.Fatalf("%s: got %x", name, got)
		}
	}
}

func TestRandomScalarReportsAFailingSource(t *testing.T) {
	if _, err := randomScalarFrom(io.LimitReader(bytes.NewReader(nil), 0)); err == nil {
		t.Fatal("randomScalarFrom swallowed a read error")
	}
}

func TestRandomScalarIsAlwaysInRange(t *testing.T) {
	n := elliptic.P256().Params().N
	for i := 0; i < 200; i++ {
		s, err := P256().RandomScalar()
		if err != nil {
			t.Fatal(err)
		}
		v := new(big.Int).SetBytes(s)
		if v.Sign() <= 0 || v.Cmp(n) >= 0 || len(s) != 32 {
			t.Fatalf("scalar %x out of range", s)
		}
	}
}
