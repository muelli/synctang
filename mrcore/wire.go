// SPDX-License-Identifier: AGPL-3.0-or-later

package mrcore

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// maxMessageSize bounds a single MR-1 wire message. Every real
// message (a request naming one point and one kid, or a response
// naming one point or x-coordinate) is a few hundred bytes at most;
// this only exists to stop a malicious or buggy peer from making
// ReadMessage allocate an unbounded amount of memory.
const maxMessageSize = 64 * 1024

// RecoverRequest is sent by the machine to a key holder to begin one
// recovery attempt: "prove you hold the key behind Kid by answering
// against this point."
type RecoverRequest struct {
	Kid []byte `json:"kid"`
	X   []byte `json:"X"`
}

// RecoverResponse is the key holder's reply to a RecoverRequest.
// Exactly one of Y or XOnly is set on success: Y from a key holder
// that holds s in the clear and can compute the full point (the file
// backend, or Tang's server); XOnly from one that can only do ECDH
// against a hardware-held key (Android Keystore, TPM2). Error is set
// instead of either if the key holder declines (kid not recognised,
// user declined the prompt).
type RecoverResponse struct {
	Y     []byte `json:"Y,omitempty"`
	XOnly []byte `json:"xOnly,omitempty"`
	Error string `json:"error,omitempty"`
}

// RecoverResult is the last message of the exchange: the machine
// telling the key holder what actually happened with the answer it
// was given.
//
// Without it a key holder knows only that its write succeeded, which
// is not the same thing at all. A relay session that has quietly died
// accepts a write and delivers it nowhere, so "answer sent" and
// "volume unlocked" look identical from the key holder's side. The
// Android app reported success on exactly that basis and told a human
// their machine had unlocked while it sat at its LUKS prompt.
//
// Sent on failure as well as success, with Error describing what went
// wrong (a secret that did not open the volume, no pending password
// request to answer), because "it did not work and here is why" is
// worth far more to whoever is holding the phone than silence.
type RecoverResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Hello is the very first message the machine sends after a key
// holder dials in, before the recover exchange itself: the name a
// human should see before approving
// anything (plan section 5: "the key holder shows the requester's
// transport ID and a name"). The transport ID itself needs no
// separate field here: it is already the cryptographically verified
// Conn.PeerID(), not merely asserted by this message.
type Hello struct {
	Name string `json:"name"`

	// Purpose is what the machine says it will do with the answer, when that is not to unlock the volume: one of
	// the Purpose constants below. Empty, and then not sent, is an ordinary unlock. It is the machine's own word
	// and an honest machine's advice to the person approving: a machine that does something else says nothing,
	// which looks exactly like an unlock. A key holder shows it as a warning, whatever it is, and never as
	// "this is only an unlock".
	Purpose string `json:"purpose,omitempty"`
}

// What a machine says it will do with the answer, other than unlock the volume. The answer is the passphrase of
// the approving key holder's own keyslot, which is good for anything a LUKS passphrase is good for.
const (
	// PurposeEnrol: enrol a key holder (synctang-unlocker enrol --approve-with-key-holder).
	PurposeEnrol = "enrol"
	// PurposePair: pair a key holder (synctang-unlocker pair --approve-with-key-holder).
	PurposePair = "pair"
	// PurposeExtract: print the passphrase of the keyslot (synctang-unlocker extract-keyslot-key).
	PurposeExtract = "extract"
)

// KnownPurpose says whether p is one of the purposes above. A key holder that meets another still treats it as
// something other than an unlock, and shows it cleaned.
func KnownPurpose(p string) bool {
	return p == PurposeEnrol || p == PurposePair || p == PurposeExtract
}

// WriteMessage writes v to w as a 4-byte big-endian length prefix
// followed by its JSON encoding, so a peer reading a stream of
// messages knows exactly where one ends and the next begins.
func WriteMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mrcore: encoding message: %w", err)
	}
	if len(body) > maxMessageSize {
		return fmt.Errorf("mrcore: message too large: %d bytes", len(body))
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("mrcore: writing message length: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mrcore: writing message body: %w", err)
	}
	return nil
}

// ReadMessage reads one message written by WriteMessage from r and
// decodes it into v.
func ReadMessage(r io.Reader, v any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return fmt.Errorf("mrcore: reading message length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length > maxMessageSize {
		return fmt.Errorf("mrcore: message too large: %d bytes", length)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return fmt.Errorf("mrcore: reading message body: %w", err)
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("mrcore: decoding message: %w", err)
	}
	return nil
}
