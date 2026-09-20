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
// user declined the prompt, wrong confirm code).
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
// holder dials in, before anything else (confirm-code or the recover
// exchange itself): the name a human should see before approving
// anything (plan section 5: "the key holder shows the requester's
// transport ID and a name"). The transport ID itself needs no
// separate field here: it is already the cryptographically verified
// Conn.PeerID(), not merely asserted by this message.
type Hello struct {
	Name string `json:"name"`
}

// ConfirmCodeChallenge is sent first, before RecoverRequest, when the
// machine is running with --confirm-code: the key holder must relay
// Code back exactly, proving whoever is answering can see the
// machine's own console, not merely reach it over the network. The
// same code is reused for every connection attempt during one agent
// run, so a human only has to read it off the console once.
type ConfirmCodeChallenge struct {
	Code string `json:"code"`
}

// ConfirmCodeResponse answers a ConfirmCodeChallenge. The machine
// must reject a response whose Code does not match exactly, before
// ever sending a RecoverRequest.
type ConfirmCodeResponse struct {
	Code string `json:"code"`
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
