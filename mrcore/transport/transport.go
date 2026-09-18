// SPDX-License-Identifier: AGPL-3.0-or-later

// Package transport connects a machine (unlocker) waiting to be unlocked
// with a key holder (Android app or other client) over an authenticated,
// bidirectional byte stream.
//
// The identity of each side is a Syncthing Device ID derived from a TLS
// certificate. A Transport implementation decides how the two sides find
// each other (relay network, direct TCP, or a future rendezvous server),
// but always produces the same Conn, with the peer's identity verified
// cryptographically rather than merely asserted.
package transport

import (
	"context"
	"io"
)

// Transport is implemented once now, against the public Syncthing relay
// network (see SyncthingRelay). A second implementation, for example a
// project-run rendezvous server, could satisfy this interface later
// without callers changing.
type Transport interface {
	// Listen announces this identity and blocks until exactly one dialer
	// connects and completes the TLS/identity handshake, or ctx is done.
	// The machine (unlocker) always listens.
	Listen(ctx context.Context, opts ListenOptions) (Conn, error)

	// Dial connects to a listening identity by its Device ID. The key
	// holder (keyholder/Android) always dials.
	Dial(ctx context.Context, opts DialOptions) (Conn, error)
}

// Conn is one authenticated, bidirectional connection to a single peer.
type Conn interface {
	// PeerID is the verified Syncthing Device ID of the other side,
	// computed from its TLS certificate, not merely asserted by it.
	PeerID() string
	io.ReadWriteCloser
}

// ListenOptions configures Transport.Listen.
type ListenOptions struct {
	// AuthorizedPeers restricts which Device IDs may complete a
	// connection; an empty slice means accept any peer that completes
	// the TLS handshake and presents a certificate. The check happens
	// strictly after the handshake, against the peer's
	// certificate-derived Device ID, never against anything the dialer
	// merely claims before then.
	AuthorizedPeers []string

	// DirectAddr, when non-empty, is a "host:port" address (for example
	// "127.0.0.1:0" for an ephemeral port) to listen on with a plain TCP
	// listener, bypassing the relay and discovery network entirely. This
	// is intended for offline tests and for local-network pairing; the
	// TLS/identity handshake still runs on top of it either way.
	DirectAddr string
}

// DialOptions configures Transport.Dial.
type DialOptions struct {
	// PeerID is the Device ID string of the identity to dial. Dial
	// verifies, after the TLS handshake, that the peer's
	// certificate-derived Device ID matches this value.
	PeerID string

	// DirectAddr, when non-empty, is a "host:port" address to dial
	// directly with a plain TCP dial, bypassing relay and discovery
	// lookup entirely.
	DirectAddr string
}
