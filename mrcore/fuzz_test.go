// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Everything fuzzed here parses bytes chosen by somebody else.
//
// ReadMessage reads whatever arrives on a connection that has been
// authenticated but not trusted: an enrolled key holder is allowed to
// talk to the machine, and a stolen or malicious one is still an
// enrolled key holder. FinishXOnly is worse, because it takes an
// x-coordinate the peer chose and lifts it to a curve point, which is
// exactly the sort of arithmetic that panics on input nobody thought
// about.
//
// The property is deliberately weak and absolute: never panic. A
// machine sitting at its LUKS prompt has no operator to restart it, so
// a panic in the agent is a machine that stays locked until somebody
// walks to it, which is the failure this whole project exists to
// avoid.

func FuzzReadMessage(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{255, 255, 255, 255, '{', '}'})
	f.Add(append([]byte{0, 0, 0, 20}, []byte(`{"name":"machine"}`)...))

	f.Fuzz(func(t *testing.T, data []byte) {
		var hello Hello
		_ = ReadMessage(bytes.NewReader(data), &hello)

		var req RecoverRequest
		_ = ReadMessage(bytes.NewReader(data), &req)

		var resp RecoverResponse
		_ = ReadMessage(bytes.NewReader(data), &resp)

		var result RecoverResult
		_ = ReadMessage(bytes.NewReader(data), &result)
	})
}

func FuzzTokenUnmarshal(f *testing.F) {
	f.Add([]byte(`{"type":"mr-1","version":1,"keyslots":[]}`))
	f.Add([]byte(`{"type":"mr-1","version":1,"keyslots":["0"],"recipients":[{"kid":"AA=="}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"mr-1","version":99999999999999999999}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var tok Token
		if err := json.Unmarshal(data, &tok); err != nil {
			return
		}
		// A token that decoded must also re-encode: enrol writes one
		// back after every change, and a token that can be read but
		// not written is a volume whose recipients can no longer be
		// edited.
		if _, err := json.Marshal(tok); err != nil {
			t.Fatalf("a token that decoded failed to re-encode: %v", err)
		}
	})
}

// FinishXOnly lifts a peer-supplied x-coordinate to a curve point and
// tries both candidate signs. Neither a wrong answer nor a malformed
// one may panic; both must simply fail.
func FuzzFinishXOnly(f *testing.F) {
	f.Add(make([]byte, 32), make([]byte, 32))
	f.Add(bytes.Repeat([]byte{0xff}, 32), bytes.Repeat([]byte{0x01}, 32))
	f.Add([]byte{}, []byte{})

	g := P256()
	s, err := g.RandomScalar()
	if err != nil {
		f.Fatalf("RandomScalar: %v", err)
	}
	S, err := g.ScalarBaseMult(s)
	if err != nil {
		f.Fatalf("ScalarBaseMult: %v", err)
	}
	rec, e := enrolForFuzz(f, g, S)

	f.Fuzz(func(t *testing.T, xOnly, scalar []byte) {
		// FinishXOnly wipes the ephemeral scalar it is given, by
		// design, so every iteration needs its own copy or all but the
		// first would be testing a run of zeroes.
		_, _ = FinishXOnly(g, append([]byte(nil), e...), rec, xOnly)
		if len(scalar) > 0 {
			_, _ = FinishXOnly(g, append([]byte(nil), scalar...), rec, xOnly)
		}
	})
}

// FuzzLiftX drives the curve-point recovery directly, since that is
// where a crafted coordinate would do its damage.
func FuzzLiftX(f *testing.F) {
	f.Add(make([]byte, 32))
	f.Add(bytes.Repeat([]byte{0xff}, 32))
	f.Add([]byte{0x02})

	g := P256()
	f.Fuzz(func(t *testing.T, x []byte) {
		evenY, oddY, err := g.LiftX(x)
		if err != nil {
			return
		}
		// Anything LiftX claims to have lifted must be a point the
		// group will accept back, or the two disagree about what is on
		// the curve.
		if _, err := g.PointToX(evenY); err != nil {
			t.Fatalf("LiftX returned an even-y point PointToX rejects: %v", err)
		}
		if _, err := g.PointToX(oddY); err != nil {
			t.Fatalf("LiftX returned an odd-y point PointToX rejects: %v", err)
		}
	})
}

func enrolForFuzz(f *testing.F, g Group, S []byte) (Recipient, []byte) {
	f.Helper()
	_, rec, err := Enrol(g, S)
	if err != nil {
		f.Fatalf("Enrol: %v", err)
	}
	e, _, err := ChallengeStart(g, rec.C)
	if err != nil {
		f.Fatalf("ChallengeStart: %v", err)
	}
	return rec, e
}
