// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"testing"
)

// T1.1: enrol then recover with a software key holder (one that holds s
// in the clear and can compute the full point Y = s.X directly, the way
// a file-backed keyholder or Tang's server does) round-trips P.
func TestEnrolRecoverFullPointRoundTrip(t *testing.T) {
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	p, rec, err := Enrol(g, S)
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}

	e, X, err := ChallengeStart(g, rec.C)
	if err != nil {
		t.Fatalf("ChallengeStart: %v", err)
	}

	// The key holder's side: it holds s, so it can compute Y = s.X
	// directly, exactly as Tang's server does.
	y, err := g.ScalarMult(X, s)
	if err != nil {
		t.Fatalf("key holder ScalarMult: %v", err)
	}

	got, err := FinishFullPoint(g, e, rec, y)
	if err != nil {
		t.Fatalf("FinishFullPoint: %v", err)
	}

	if !bytes.Equal(got, p) {
		t.Fatalf("recovered secret does not match enrolled secret:\n got=%x\n want=%x", got, p)
	}
}

// T1.2: recover via the x-only key holder path (Android Keystore or
// TPM2 ECDH, which return only the x-coordinate of s.X). The machine
// must lift x to both sign candidates and try each; exactly one
// authenticates.
func TestEnrolRecoverXOnlyRoundTrip(t *testing.T) {
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}

	p, rec, err := Enrol(g, S)
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}

	e, X, err := ChallengeStart(g, rec.C)
	if err != nil {
		t.Fatalf("ChallengeStart: %v", err)
	}

	// The key holder's side: it can only return x(s.X), as an ECDH call
	// against a hardware-held key would.
	y, err := g.ScalarMult(X, s)
	if err != nil {
		t.Fatalf("key holder ScalarMult: %v", err)
	}
	xOnly, err := g.PointToX(y)
	if err != nil {
		t.Fatalf("PointToX: %v", err)
	}

	got, err := FinishXOnly(g, e, rec, xOnly)
	if err != nil {
		t.Fatalf("FinishXOnly: %v", err)
	}

	if !bytes.Equal(got, p) {
		t.Fatalf("recovered secret does not match enrolled secret:\n got=%x\n want=%x", got, p)
	}
}

// T1.3: a wrong key holder (or an attacker guessing) must fail with an
// authentication error, never a panic, on both the full-point and
// x-only paths.
func TestRecoverWrongKeyFails(t *testing.T) {
	g := P256()

	s, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar s: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		t.Fatalf("ScalarBaseMult S: %v", err)
	}

	_, rec, err := Enrol(g, S)
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}

	e, X, err := ChallengeStart(g, rec.C)
	if err != nil {
		t.Fatalf("ChallengeStart: %v", err)
	}

	wrongS, err := g.RandomScalar()
	if err != nil {
		t.Fatalf("RandomScalar wrongS: %v", err)
	}
	if bytes.Equal(wrongS, s) {
		t.Fatal("wrong scalar collided with the real one, rerun")
	}

	wrongY, err := g.ScalarMult(X, wrongS)
	if err != nil {
		t.Fatalf("ScalarMult wrongY: %v", err)
	}

	t.Run("full point", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("FinishFullPoint panicked on a wrong key: %v", r)
			}
		}()
		_, err := FinishFullPoint(g, e, rec, wrongY)
		if err == nil {
			t.Fatal("FinishFullPoint succeeded with a wrong key holder")
		}
	})

	wrongXOnly, err := g.PointToX(wrongY)
	if err != nil {
		t.Fatalf("PointToX: %v", err)
	}

	t.Run("x only", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("FinishXOnly panicked on a wrong key: %v", r)
			}
		}()
		_, err := FinishXOnly(g, e, rec, wrongXOnly)
		if err == nil {
			t.Fatal("FinishXOnly succeeded with a wrong key holder")
		}
	})
}

// Two enrolments for two different recipients over the same secret
// must both recover it independently (recipients are first class).
func TestMultipleRecipients(t *testing.T) {
	g := P256()

	s1, _ := g.RandomScalar()
	S1, _ := g.ScalarBaseMult(s1)
	s2, _ := g.RandomScalar()
	S2, _ := g.ScalarBaseMult(s2)

	p1, rec1, err := Enrol(g, S1)
	if err != nil {
		t.Fatalf("Enrol recipient 1: %v", err)
	}
	p2, rec2, err := Enrol(g, S2)
	if err != nil {
		t.Fatalf("Enrol recipient 2: %v", err)
	}

	if bytes.Equal(p1, p2) {
		t.Fatal("two independent enrolments produced the same secret")
	}
	if bytes.Equal(rec1.Kid, rec2.Kid) {
		t.Fatal("two different recipients got the same kid")
	}

	e1, X1, err := ChallengeStart(g, rec1.C)
	if err != nil {
		t.Fatalf("ChallengeStart 1: %v", err)
	}
	y1, err := g.ScalarMult(X1, s1)
	if err != nil {
		t.Fatalf("ScalarMult 1: %v", err)
	}
	got1, err := FinishFullPoint(g, e1, rec1, y1)
	if err != nil {
		t.Fatalf("FinishFullPoint 1: %v", err)
	}
	if !bytes.Equal(got1, p1) {
		t.Fatal("recipient 1 did not recover its own secret")
	}

	e2, X2, err := ChallengeStart(g, rec2.C)
	if err != nil {
		t.Fatalf("ChallengeStart 2: %v", err)
	}
	y2, err := g.ScalarMult(X2, s2)
	if err != nil {
		t.Fatalf("ScalarMult 2: %v", err)
	}
	got2, err := FinishFullPoint(g, e2, rec2, y2)
	if err != nil {
		t.Fatalf("FinishFullPoint 2: %v", err)
	}
	if !bytes.Equal(got2, p2) {
		t.Fatal("recipient 2 did not recover its own secret")
	}
}
