// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// T1.5: fixed test vectors in testdata/mr1.json, shared with the
// Android/Kotlin instrumented tests (WP5), so both implementations are
// checked against the same numbers rather than only against each
// other's bugs.
type vectorFile struct {
	Recipient struct {
		S string `json:"S"`
	} `json:"recipient"`
	Enrol struct {
		C     string `json:"C"`
		Kid   string `json:"kid"`
		P     string `json:"P"`
		Nonce string `json:"nonce"`
		Ct    string `json:"ct"`
	} `json:"enrol"`
	Recover struct {
		E     string `json:"e_scalar"`
		X     string `json:"X"`
		Y     string `json:"Y"`
		XOnly string `json:"xOnly"`
	} `json:"recover"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile("../testdata/mr1.json")
	if err != nil {
		t.Fatalf("reading testdata/mr1.json: %v", err)
	}
	var v vectorFile
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing testdata/mr1.json: %v", err)
	}
	return v
}

func hexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding hex %q: %v", s, err)
	}
	return b
}

func TestVectorsFullPointRecovery(t *testing.T) {
	v := loadVectors(t)
	g := P256()

	rec := Recipient{
		Kid:        hexDecode(t, v.Enrol.Kid),
		S:          hexDecode(t, v.Recipient.S),
		C:          hexDecode(t, v.Enrol.C),
		Ciphertext: hexDecode(t, v.Enrol.Ct),
		Nonce:      hexDecode(t, v.Enrol.Nonce),
	}
	e := hexDecode(t, v.Recover.E)
	y := hexDecode(t, v.Recover.Y)
	wantP := hexDecode(t, v.Enrol.P)

	got, err := FinishFullPoint(g, e, rec, y)
	if err != nil {
		t.Fatalf("FinishFullPoint: %v", err)
	}
	if !bytes.Equal(got, wantP) {
		t.Fatalf("recovered secret does not match vector:\n got=%x\n want=%x", got, wantP)
	}
}

func TestVectorsXOnlyRecovery(t *testing.T) {
	v := loadVectors(t)
	g := P256()

	rec := Recipient{
		Kid:        hexDecode(t, v.Enrol.Kid),
		S:          hexDecode(t, v.Recipient.S),
		C:          hexDecode(t, v.Enrol.C),
		Ciphertext: hexDecode(t, v.Enrol.Ct),
		Nonce:      hexDecode(t, v.Enrol.Nonce),
	}
	e := hexDecode(t, v.Recover.E)
	xOnly := hexDecode(t, v.Recover.XOnly)
	wantP := hexDecode(t, v.Enrol.P)

	got, err := FinishXOnly(g, e, rec, xOnly)
	if err != nil {
		t.Fatalf("FinishXOnly: %v", err)
	}
	if !bytes.Equal(got, wantP) {
		t.Fatalf("recovered secret does not match vector:\n got=%x\n want=%x", got, wantP)
	}
}

// The vector's X must equal C + E, and Y must equal x(vector's Y) once
// lifted back from xOnly: these cross-checks catch a corrupted or
// hand-edited vector file, independent of the recovery functions
// under test above.
func TestVectorsInternalConsistency(t *testing.T) {
	v := loadVectors(t)
	g := P256()

	C := hexDecode(t, v.Enrol.C)
	e := hexDecode(t, v.Recover.E)
	wantX := hexDecode(t, v.Recover.X)
	wantY := hexDecode(t, v.Recover.Y)
	wantXOnly := hexDecode(t, v.Recover.XOnly)

	E, err := g.ScalarBaseMult(e)
	if err != nil {
		t.Fatalf("ScalarBaseMult: %v", err)
	}
	X, err := g.Add(C, E)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !bytes.Equal(X, wantX) {
		t.Fatalf("C + E != vector X:\n got=%x\n want=%x", X, wantX)
	}

	xOnly, err := g.PointToX(wantY)
	if err != nil {
		t.Fatalf("PointToX: %v", err)
	}
	if !bytes.Equal(xOnly, wantXOnly) {
		t.Fatalf("x(Y) != vector xOnly:\n got=%x\n want=%x", xOnly, wantXOnly)
	}
}
