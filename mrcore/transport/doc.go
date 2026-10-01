// SPDX-License-Identifier: AGPL-3.0-or-later

// Package transport connects two parties, one waiting and one dialling,
// over an authenticated, bidirectional byte stream, with no server of
// its own to run.
//
// The identity of each side is a Syncthing Device ID derived from a TLS
// certificate (see LoadOrCreateCert). A Transport implementation decides
// how the two sides find each other, but always produces the same Conn,
// with the peer's identity verified cryptographically after the TLS
// handshake rather than merely asserted.
//
// # Roles
//
// The waiting side calls Transport.Listen and blocks until exactly one
// peer connects and completes the handshake. The dialling side calls
// Transport.Dial with the listener's Device ID. Which role a party plays
// is fixed by the application, not negotiated.
//
// # Implementations
//
//   - SyncthingRelay finds the peer through the public Syncthing global
//     discovery server and relay pool, so it works across NATs given an
//     Internet route.
//   - LocalDiscovery finds the peer by multicast on the local network,
//     so it works with no Internet route at all.
//   - Multi races several Transports and keeps the first connection to
//     complete. When listening it restarts a leg that fails, which
//     matters: a dynamic relay client walks its relay list once and then
//     gives up for good, so the restart, which builds a fresh client, is
//     what keeps a long wait healthy. Dial does not restart failed legs;
//     the caller retries the whole attempt.
//
// Setting DirectAddr in ListenOptions and DialOptions bypasses discovery
// and relays and uses a plain TCP connection; the TLS and identity
// handshake still runs on top. It is meant for tests and for
// local-network pairing.
//
// # What this package does not do
//
// It carries bytes between two authenticated identities and nothing
// more. It does not frame messages, does not define what the peers say
// to each other, and does not decide which Device IDs to trust: that is
// ListenOptions.AuthorizedPeers on the listening side and
// DialOptions.PeerID on the dialling side.
//
// # Network dependencies
//
// SyncthingRelay talks to infrastructure it does not control: the
// Syncthing discovery servers (announce and lookup are different
// endpoints) and the public relay pool. Announcements are refreshed
// lazily (every thirty minutes when nothing has changed) because these
// are shared public servers.
package transport
