// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"io"
)

// Transport is implemented once now, against the public Syncthing relay
// network (see SyncthingRelay). A second implementation, for example a
// project-run rendezvous server, could satisfy this interface later
// without callers changing.
type Transport interface {
	// Listen announces this identity and blocks until exactly one dialer
	// connects and completes the TLS/identity handshake, or ctx is done.
	// The machine (synctang-unlocker) always listens.
	Listen(ctx context.Context, opts ListenOptions) (Conn, error)

	// Dial connects to a listening identity by its Device ID. The key
	// holder (synctang-keyholder/Android) always dials.
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

// ErrNoPeerID is what Dial returns when DialOptions.PeerID is empty. A
// dial that pins no identity would complete its handshake with whoever
// answered, so it is refused instead of being left to each caller to get
// right.
var ErrNoPeerID = errors.New("transport: Dial needs a PeerID to pin the peer's identity")

// DialOptions configures Transport.Dial.
type DialOptions struct {
	// PeerID is the Device ID string of the identity to dial. It must
	// not be empty (Dial returns ErrNoPeerID). Dial verifies, after the
	// TLS handshake, that the peer's certificate-derived Device ID
	// matches this value.
	PeerID string

	// DirectAddr, when non-empty, is a "host:port" address to dial
	// directly with a plain TCP dial, bypassing relay and discovery
	// lookup entirely.
	DirectAddr string
}

// Route is which way a dial is trying to reach the other side, for a
// caller that wants to say so.
type Route string

const (
	// RouteRelay: through a relay, found by looking the peer up on
	// global discovery.
	RouteRelay Route = "relay"
	// RouteLocal: straight over the local network, found by multicast.
	RouteLocal Route = "local"
	// RouteDirect: a plain TCP address, either given by the caller or
	// the one global discovery returned.
	RouteDirect Route = "direct"
)

// DialPhase is how far a dial has got on a Route.
type DialPhase int

const (
	// PhaseLookup: finding out where the peer is (a discovery lookup, or
	// listening for a local announcement).
	PhaseLookup DialPhase = iota + 1
	// PhaseConnect: reaching that address (joining the relay session, or
	// opening the TCP connection).
	PhaseConnect
	// PhaseHandshake: connected; the TLS handshake that checks the
	// peer's Device ID is running.
	PhaseHandshake
)

func (p DialPhase) String() string {
	switch p {
	case PhaseLookup:
		return "lookup"
	case PhaseConnect:
		return "connect"
	case PhaseHandshake:
		return "handshake"
	}
	return "unknown"
}

// DialProgress is told, as a Dial goes, which phase it has reached on which
// route. It is called from the dialling goroutine and, for Multi, from
// several at once, so it must be quick and safe for concurrent use. It is
// optional: it changes nothing about how the dial behaves.
type DialProgress func(route Route, phase DialPhase)
