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
