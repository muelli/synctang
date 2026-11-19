// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
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
const pairProofLabel = "synctang-pair-v3"

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
// Bound to both Device IDs, to the nonce the machine chose for this
// connection and the nonce the key holder committed to (holderNonce), and to the public key being enrolled, so a proof is worth
// nothing anywhere else: not replayed on a later connection (the nonce
// differs), not reused by a second phone that photographed the same code
// (the peer ID differs), not replayed against another machine that
// happened to show the same code (the machine ID differs), and not
// usable to enrol a different key from the one it was made for (the
// public key differs). Binding the key is what makes the proof vouch for
// the one thing the machine is about to write to disk.
//
// Both Device IDs are the cryptographically verified ones from the
// completed TLS handshake, never anything a peer merely claimed in a
// message, so an attacker cannot choose what its own proof is bound to.
func PairProof(secret []byte, machineID, peerID string, pubKey, nonce, holderNonce []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	writeParts(mac, []byte(pairProofLabel), []byte(machineID), []byte(peerID), pubKey, nonce, holderNonce)
	return mac.Sum(nil)
}

// writeParts writes the parts to w, each preceded by its length as eight bytes, so that no two different
// tuples can produce the same input: a byte moved from one part to the next changes it.
func writeParts(w interface{ Write([]byte) (int, error) }, parts ...[]byte) {
	for _, part := range parts {
		var lenBuf [8]byte
		for i := 0; i < 8; i++ {
			lenBuf[7-i] = byte(len(part) >> (8 * i))
		}
		w.Write(lenBuf[:])
		w.Write(part)
	}
}

// pairCommitLabel and pairRequestLabel keep the commitment and the tag of a request from ever being the same
// bytes as a proof or a code made from the same fields.
const (
	pairCommitLabel  = "synctang-pair-commit-v1"
	pairRequestLabel = "synctang-pair-req-v1"
)

// PairHolderNonceLen is the length of the nonce a key holder draws for one connection.
const PairHolderNonceLen = 32

// PairCommit is what a key holder sends in its request in place of its nonce: a hash of the machine, itself,
// its key and the nonce, so that it cannot choose another nonce after it has seen the machine's. It reveals
// nothing about the nonce.
func PairCommit(machineID, peerID string, pubKey, holderNonce []byte) []byte {
	h := sha256.New()
	writeParts(h, []byte(pairCommitLabel), []byte(machineID), []byte(peerID), pubKey, holderNonce)
	return h.Sum(nil)
}

// PairRequestTag is what shows the machine, before it gives out a nonce, that a request comes from somebody
// who has the pairing secret. It covers the commitment, so that nobody can swap in a commitment of their own.
func PairRequestTag(secret []byte, machineID, peerID string, pubKey, commit []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	writeParts(mac, []byte(pairRequestLabel), []byte(machineID), []byte(peerID), pubKey, commit)
	return mac.Sum(nil)
}

// pairSASLabel is mixed into the short code so that it cannot collide with another use of SHA-256 over
// the same fields.
const pairSASLabel = "synctang-pair-sas-v2"

// PairSAS is the short code both the machine and the key holder show after a pairing request, for the
// person to compare: six digits, in two groups.
//
// The pairing code authenticates the key holder to the machine, and that is all it does: whoever has
// the code first is enrolled if the operator says yes. If the code leaks (a photographed QR code, a
// pasted link), the operator has to be able to tell the key holder in front of them from somebody
// else's, and this is how. It is derived from the machine's and the key holder's Device IDs, the key
// being enrolled, the nonce the machine chose for this connection and the nonce the key holder chose,
// so each side can compute it with nothing sent for the purpose.
//
// Why neither side can make it match. The key holder's request carries a commitment to its nonce
// (PairCommit) before the machine's nonce exists, and the nonce itself is revealed only after the
// machine's has been sent. So whoever plays the machine has to choose its nonce without knowing the key
// holder's, and whoever plays the key holder has to choose its own before it has seen the machine's:
// either way the code is out of their reach, and a guess has one chance in a million. With only one side
// committed (the key holder, as the first version did) the other side chooses last and can fit the code
// to any value in about a million tries: a link passed on with another machine's identity, and the real
// machine asked to enrol the attacker's key, made both codes agree (found in a review and run, 2026-11-19).
//
// It is not secret, and deliberately does not include the pairing code.
func PairSAS(machineID, peerID string, pubKey, nonce, holderNonce []byte) string {
	h := sha256.New()
	writeParts(h, []byte(pairSASLabel), []byte(machineID), []byte(peerID), pubKey, nonce, holderNonce)
	sum := h.Sum(nil)
	n := (uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])) % 1_000_000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

// The messages of a pairing, in the order they are sent. The order is part of what makes the short code
// (PairSAS) mean something: each side commits to what it contributes before it sees the other's.
//
//	key holder -> machine   PairRequest   the key to enrol, a commitment to the key holder's nonce, and a tag
//	                                      that shows it has the pairing secret; sent first
//	machine -> key holder   PairChallenge a fresh nonce, chosen after the commitment was seen
//	key holder -> machine   PairResponse  the key holder's nonce, revealed, and the proof of the pairing code
//	machine -> key holder   PairResult    the verdict
//
// A machine that will not go on sends a PairChallenge with Error set instead of a nonce.

// PairRequest is the key holder asking to be enrolled. PubKey is its long-term MR-1 public key S; Name is a
// label for a human to recognise it by (the machine shows it, cleaned, beside the Device ID). Its transport
// identity is deliberately absent: the machine takes that from the verified Conn.PeerID() rather than from
// anything claimed here, so a key holder cannot ask to be enrolled under somebody else's identity.
//
// Commit is PairCommit of the key holder's own nonce, and Tag is PairRequestTag over it: the machine checks
// the tag before it gives out a nonce, so somebody who does not have the pairing secret gets neither a
// nonce nor a code.
type PairRequest struct {
	PubKey []byte `json:"pubkey"`
	Name   string `json:"name,omitempty"`
	Commit []byte `json:"commit"`
	Tag    []byte `json:"tag"`
}

// PairChallenge is the machine's answer to a PairRequest: a fresh random nonce, chosen after the key
// holder's key and commitment were known, that the key holder must bind its proof to. Name is the machine's
// own name, a label. Error, when set, is the machine's refusal to go on, in place of a nonce.
type PairChallenge struct {
	Nonce []byte `json:"nonce"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error,omitempty"`
}

// PairNonceLen is the length of the nonce a machine sends, and the least a key holder accepts: a machine that
// sent a short one (or none) would take the freshness out of the proof and of the short code.
const PairNonceLen = 32

// PairMinNonceLen is the shortest nonce a key holder accepts.
const PairMinNonceLen = 16

// PairResponse is the key holder's nonce, revealed, and its proof that it holds the pairing code (see
// PairProof).
type PairResponse struct {
	HolderNonce []byte `json:"holder_nonce"`
	Proof       []byte `json:"proof"`
}

// PairResult is the machine's verdict, sent whether or not the
// enrolment happened, so the phone can say what went wrong rather than
// simply timing out.
type PairResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// The reasons a machine gives in PairResult.Error. Defined once here,
// beside the message, so that the key holder can tell them apart and
// explain each in its own words without the two sides' wording
// drifting. Any other reason is still shown as the machine sent it.
const (
	PairRefusedCodeMismatch = "the pairing code did not match"
	PairRefusedInvalidKey   = "the public key offered is not a valid P-256 point"
	PairRefusedDeclined     = "the operator declined the pairing"
	PairRefusedEnrolFailed  = "the machine could not enrol this key holder"
	PairRefusedProtocol     = "the pairing messages did not fit together"
	PairRefusedTooMany      = "too many connections used the pairing code and left, so it is no longer good"
)

// Errors the machine's checks return, for the caller to turn into a refusal and a log line.
var (
	ErrPairRequestTag = errors.New("mrcore: the pairing request does not carry a valid tag, so it did not come from somebody with the pairing code")
	ErrPairCommitment = errors.New("mrcore: the key holder's nonce is not the one it committed to")
	ErrPairProof      = errors.New("mrcore: the proof of the pairing code is not valid")
	ErrPairShortNonce = errors.New("mrcore: the machine's nonce is too short to bind anything to")
)

// PairRefusal is a machine's refusal to go on, as it reached the key holder in place of a challenge.
type PairRefusal struct{ Reason string }

func (e *PairRefusal) Error() string { return "mrcore: the machine refused the pairing: " + e.Reason }

// PairHolder is the key holder's side of one pairing connection: it draws the nonce the key holder
// commits to, builds the request and answers the challenge. One per connection: a new attempt is a new nonce.
type PairHolder struct {
	secret            []byte
	machineID, selfID string
	pub               []byte
	name              string
	holderNonce       []byte
}

// NewPairHolder starts a connection of a key holder with Device ID selfID to the machine machineID, to enrol
// pub. The secret is the pairing secret; the holder does not keep a copy of it beyond what Wipe clears.
func NewPairHolder(secret []byte, machineID, selfID string, pub []byte, name string) (*PairHolder, error) {
	n := make([]byte, PairHolderNonceLen)
	if _, err := rand.Read(n); err != nil {
		return nil, fmt.Errorf("mrcore: drawing a pairing nonce: %w", err)
	}
	return &PairHolder{secret: append([]byte(nil), secret...), machineID: machineID, selfID: selfID, pub: pub, name: name, holderNonce: n}, nil
}

// Request is what the key holder sends first.
func (h *PairHolder) Request() PairRequest {
	commit := PairCommit(h.machineID, h.selfID, h.pub, h.holderNonce)
	return PairRequest{PubKey: h.pub, Name: h.name, Commit: commit,
		Tag: PairRequestTag(h.secret, h.machineID, h.selfID, h.pub, commit)}
}

// Answer checks the machine's challenge and returns what to send back and the short code to show. A challenge
// that carries a refusal gives a *PairRefusal; one with a nonce too short to bind anything to gives
// ErrPairShortNonce.
func (h *PairHolder) Answer(c PairChallenge) (PairResponse, string, error) {
	if c.Error != "" {
		return PairResponse{}, "", &PairRefusal{Reason: c.Error}
	}
	if len(c.Nonce) < PairMinNonceLen {
		return PairResponse{}, "", ErrPairShortNonce
	}
	return PairResponse{
		HolderNonce: h.holderNonce,
		Proof:       PairProof(h.secret, h.machineID, h.selfID, h.pub, c.Nonce, h.holderNonce),
	}, PairSAS(h.machineID, h.selfID, h.pub, c.Nonce, h.holderNonce), nil
}

// Wipe clears the secret and the nonce.
func (h *PairHolder) Wipe() {
	Wipe(h.secret)
	Wipe(h.holderNonce)
}

// CheckPairRequest is the machine's first check, before it gives out a nonce: the tag shows the key holder
// has the pairing secret and made this commitment. peerID is the Device ID of the verified connection.
func CheckPairRequest(secret []byte, machineID, peerID string, req PairRequest) error {
	if !hmac.Equal(PairRequestTag(secret, machineID, peerID, req.PubKey, req.Commit), req.Tag) {
		return ErrPairRequestTag
	}
	return nil
}

// CheckPairResponse is the machine's second check: the nonce the key holder reveals is the one it committed
// to, and the proof is the one for this connection. It returns the short code for the operator to compare.
func CheckPairResponse(secret []byte, machineID, peerID string, req PairRequest, nonce []byte, resp PairResponse) (string, error) {
	if len(resp.HolderNonce) != PairHolderNonceLen ||
		!hmac.Equal(PairCommit(machineID, peerID, req.PubKey, resp.HolderNonce), req.Commit) {
		return "", ErrPairCommitment
	}
	if !hmac.Equal(PairProof(secret, machineID, peerID, req.PubKey, nonce, resp.HolderNonce), resp.Proof) {
		return "", ErrPairProof
	}
	return PairSAS(machineID, peerID, req.PubKey, nonce, resp.HolderNonce), nil
}
