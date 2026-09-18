// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

// hkdfInfo is fixed by the protocol: both enrolment and every recovery
// attempt must derive k the same way, or recovery can never succeed.
const hkdfInfo = "mr-1 enrol"

const (
	secretLen = 32 // the recovered LUKS volume secret P
	nonceLen  = 12 // AES-256-GCM standard nonce size
)

// Recipient is one entry in an enrolled LUKS2 mr-1 token: the encrypted
// share for a single key holder. Nothing in it is secret on its own;
// that mirrors Tang's property that the header is useless without the
// key holder.
type Recipient struct {
	Kid        []byte // sha256(S), identifies the key holder
	S          []byte // the key holder's long-term public point, s.G
	C          []byte // the enrolment-time ephemeral point, c.G
	Ciphertext []byte // AEAD(k, nonce, P)
	Nonce      []byte
}

// Enrol creates a new random LUKS volume secret P and a Recipient entry
// that only the holder of the private scalar behind S can recover P
// from. The caller is responsible for adding P as a LUKS2 keyslot and
// storing the Recipient in the LUKS2 token; mrcore does no disk I/O and
// keeps no secret after returning.
func Enrol(g Group, s []byte) (p []byte, rec Recipient, err error) {
	secret := make([]byte, secretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: generating secret: %w", err)
	}

	c, err := g.RandomScalar()
	if err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: enrol: %w", err)
	}
	defer wipe(c)

	C, err := g.ScalarBaseMult(c)
	if err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: enrol: %w", err)
	}

	K, err := g.ScalarMult(s, c)
	if err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: enrol: %w", err)
	}
	defer wipe(K)

	kid := sha256.Sum256(s)

	k, err := deriveKey(g, K, kid[:])
	if err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: enrol: %w", err)
	}
	defer wipe(k)

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: generating nonce: %w", err)
	}

	ct, err := aeadSeal(k, nonce, secret)
	if err != nil {
		return nil, Recipient{}, fmt.Errorf("mrcore: enrol: %w", err)
	}

	return secret, Recipient{
		Kid:        kid[:],
		S:          append([]byte(nil), s...),
		C:          C,
		Ciphertext: ct,
		Nonce:      nonce,
	}, nil
}

// ChallengeStart begins a recovery attempt. It returns the ephemeral
// scalar e, which the machine must keep secret and wipe after the
// attempt, and X = C + E, which is sent to the key holder alongside
// Kid.
func ChallengeStart(g Group, c []byte) (e, x []byte, err error) {
	e, err = g.RandomScalar()
	if err != nil {
		return nil, nil, fmt.Errorf("mrcore: challenge start: %w", err)
	}

	E, err := g.ScalarBaseMult(e)
	if err != nil {
		wipe(e)
		return nil, nil, fmt.Errorf("mrcore: challenge start: %w", err)
	}

	X, err := g.Add(c, E)
	if err != nil {
		wipe(e)
		return nil, nil, fmt.Errorf("mrcore: challenge start: %w", err)
	}

	return e, X, nil
}

// FinishFullPoint completes a recovery attempt when the key holder
// returned the full point Y = s.X directly, the way a file-backed
// keyholder or Tang's server does. e is the ephemeral scalar from the
// matching ChallengeStart call; it is wiped before returning.
func FinishFullPoint(g Group, e []byte, rec Recipient, y []byte) (p []byte, err error) {
	defer wipe(e)

	eS, err := g.ScalarMult(rec.S, e)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}
	defer wipe(eS)

	return finishWithY(g, rec, eS, y)
}

// FinishXOnly completes a recovery attempt when the key holder returned
// only the x-coordinate of s.X, as an Android Keystore or TPM2 ECDH
// call would. It lifts x to both sign candidates and tries each in
// turn; the AEAD tag picks the right one. e is the ephemeral scalar
// from the matching ChallengeStart call; it is wiped before returning.
func FinishXOnly(g Group, e []byte, rec Recipient, xOnly []byte) (p []byte, err error) {
	defer wipe(e)

	eS, err := g.ScalarMult(rec.S, e)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}
	defer wipe(eS)

	evenY, oddY, err := g.LiftX(xOnly)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}

	if p, err := finishWithY(g, rec, eS, evenY); err == nil {
		return p, nil
	}
	return finishWithY(g, rec, eS, oddY)
}

// finishWithY computes K' = Y - eS, derives k' the same way enrolment
// derived k, and opens the AEAD. A wrong Y (wrong key holder, or the
// wrong sign candidate) makes K' wrong, so the AEAD tag fails to
// authenticate; that failure is reported as an error, never a panic.
func finishWithY(g Group, rec Recipient, eS, y []byte) (p []byte, err error) {
	negEs, err := g.Negate(eS)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}

	Kp, err := g.Add(y, negEs)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}
	defer wipe(Kp)

	k, err := deriveKey(g, Kp, rec.Kid)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: %w", err)
	}
	defer wipe(k)

	secret, err := aeadOpen(k, rec.Nonce, rec.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("mrcore: finish: authentication failed: %w", err)
	}
	return secret, nil
}

// deriveKey turns a shared point K into the AEAD key both enrolment
// and recovery use. Only the x-coordinate is used, matching what an
// x-only key holder is able to supply.
func deriveKey(g Group, k, kid []byte) ([]byte, error) {
	xK, err := g.PointToX(k)
	if err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}
	defer wipe(xK)

	key, err := hkdf.Key(sha256.New, xK, kid, hkdfInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}
	return key, nil
}

func aeadSeal(key, nonce, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nil
}

func aeadOpen(key, nonce, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	return gcm, nil
}

// wipe zeroes b in place. It is best effort: Go's garbage collector may
// have already copied the bytes elsewhere, but zeroing the slice we
// still hold is cheap and removes one copy from memory promptly.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
