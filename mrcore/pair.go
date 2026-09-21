// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// Pairing exists because enrolment needs the key holder's public key to
// reach the machine, and that is the awkward direction: a machine at a
// LUKS prompt has a screen but no camera, while a phone has both. So
// the QR code carries what the machine can show (its Device ID and a
// one-time secret) and the key material travels the other way over the
// ordinary transport, rather than being copied by hand.
//
// Note what deliberately does not travel: the volume's passphrase.
// syncthing-socket's equivalent QR code carries the passphrase itself,
// which is what lets a single scan finish its enrolment, and its own
// setup script warns not to photograph the code for exactly that
// reason. MR-1 never puts the passphrase on the phone in any form, so
// the same shortcut is not available and pairing has to be a short
// conversation instead of a one-way transfer.

// pairingSecretLen is the length of a pairing secret in bytes. 16
// bytes is 128 bits, which is far beyond guessing inside a pairing
// window measured in minutes, and encodes to 26 characters: short
// enough to type off a console when a camera is not an option.
const pairingSecretLen = 16

// pairProofLabel is mixed into every proof so a value computed here
// can never collide with an HMAC computed for some other purpose with
// the same secret.
const pairProofLabel = "synctang-pair-v1"

// pairingEncoding is base32 without padding: unambiguous when read off
// a screen, safe in a URL query without escaping, and accepted in any
// case by DecodePairingSecret.
var pairingEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewPairingSecret returns a fresh pairing secret and its encoded form,
// the latter being what goes in the QR code and on the console.
func NewPairingSecret() (secret []byte, encoded string, err error) {
	secret = make([]byte, pairingSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", fmt.Errorf("mrcore: generating a pairing secret: %w", err)
	}
	return secret, pairingEncoding.EncodeToString(secret), nil
}

// DecodePairingSecret parses the encoded form, in whatever case it was
// typed in.
func DecodePairingSecret(encoded string) ([]byte, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(encoded))
	secret, err := pairingEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("mrcore: invalid pairing secret: %w", err)
	}
	if len(secret) != pairingSecretLen {
		return nil, fmt.Errorf("mrcore: pairing secret is %d bytes, want %d", len(secret), pairingSecretLen)
	}
	return secret, nil
}

// PairProof is what the key holder sends to show it holds the secret
// from the machine's QR code.
//
// Bound to both Device IDs and to a nonce the machine chose for this
// connection, so a proof is worth nothing anywhere else: not replayed
// on a later connection (the nonce differs), not reused by a second
// phone that photographed the same code (the peer ID differs), and not
// replayed against another machine that happened to show the same code
// (the machine ID differs).
//
// Both Device IDs are the cryptographically verified ones from the
// completed TLS handshake, never anything a peer merely claimed in a
// message, so an attacker cannot choose what its own proof is bound to.
func PairProof(secret []byte, machineID, peerID string, nonce []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	// Length-prefixed rather than concatenated, so that no two
	// different triples can produce the same input to the MAC.
	for _, part := range [][]byte{[]byte(pairProofLabel), []byte(machineID), []byte(peerID), nonce} {
		var lenBuf [8]byte
		for i := 0; i < 8; i++ {
			lenBuf[7-i] = byte(len(part) >> (8 * i))
		}
		mac.Write(lenBuf[:])
		mac.Write(part)
	}
	return mac.Sum(nil)
}

// PairChallenge is the machine's first message on a pairing
// connection: a fresh random nonce the key holder must bind its proof
// to.
type PairChallenge struct {
	Nonce []byte `json:"nonce"`
	Name  string `json:"name,omitempty"`
}

// PairRequest is the key holder asking to be enrolled. PubKey is its
// long-term MR-1 public key S; Name is a label for a human to
// recognise it by. Its transport identity is deliberately absent: the
// machine takes that from the verified Conn.PeerID() rather than from
// anything claimed here, so a key holder cannot ask to be enrolled
// under somebody else's identity.
type PairRequest struct {
	PubKey []byte `json:"pubkey"`
	Name   string `json:"name,omitempty"`
	Proof  []byte `json:"proof"`
}

// PairResult is the machine's verdict, sent whether or not the
// enrolment happened, so the phone can say what went wrong rather than
// simply timing out.
type PairResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}
