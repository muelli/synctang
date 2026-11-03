<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Transport: local network and the Internet, at once

`synctang-unlocker agent` and `synctang-keyholder unlock` do not have to guess in advance
whether the Internet is reachable. Unless `--direct-addr` is given
(which bypasses both, for offline tests and manual same-network
pairing), both sides run two transports at once and use whichever
finds the other first:

- `transport.LocalDiscovery`: the machine multicasts its Device ID and
  a plain TCP listener's address on the local network every couple of
  seconds; the key holder listens for a matching announcement and
  dials it directly. Needs a LAN link and nothing else: no DNS, no
  route to the Internet, no third-party infrastructure.
- `transport.SyncthingRelay`: the public Syncthing relay network and
  global discovery, for when the two sides are not on the same
  network. An owner can name a relay pool and discovery server of their
  own instead; see [private-servers.md](private-servers.md).

The Android app races the same two transports (`mobile/mobile.go`'s
`newDialTransport`), so a phone can unlock a machine on the same
network with no Internet at all. Android filters multicast out before
it reaches an application unless a `MulticastLock` is held, so the app
declares `CHANGE_WIFI_MULTICAST_STATE` (a normal permission, granted at
install time with no runtime prompt) and holds a lock for the duration
of a dial and no longer, since holding one keeps the Wi-Fi chip out of
its power-saving filter. A device that will not give out a lock loses
local discovery and keeps the relay: `MulticastLease` degrades rather
than breaks. Both sides must share a multicast domain, and local
discovery is IPv4 only.

`transport.Multi` races the two: on a LAN with no Internet route, the
relay path simply keeps failing in the background while local
discovery succeeds, so unlock keeps working; with the Internet
reachable, both are tried and whichever answers first wins, so this
costs nothing when local discovery is not needed at all. Either way
the TLS handshake and Device ID pinning that follow are identical: an
announcement (local or global) is only ever a hint about where to
dial, never trusted on its own, exactly as the plan's threat model
requires (see [threat-model.md](threat-model.md)).

## Joining the relay pool

A machine that listens through the public pool has to join one relay before a
key holder can reach it. Syncthing's own client for a pool measures the latency
to every relay on the list one after another and only then joins one. The pool
lists about a hundred relays, so on a Fedora 44 VM (2026-11-03) the time from
"relay client created" to "joined relay" was 19.9 seconds `[V]` (trace log of
one run: a second for the list, the rest measuring), and a phone that tapped
Unlock in that time saw "Reaching the machine" until the machine was ready. The
same dial with the machine already waiting took 1.7 seconds `[V]`.

`poolClient` (`mrcore/transport/relaypool.go`) takes the place of that client
for a `dynamic+http(s)://` pool URL. It fetches the same list and walks it the
same way, nearest first in buckets of 50 ms and shuffled within a bucket, moving
on when a relay gives up and failing once every relay has been tried, but it
measures all the relays at once, each for at most two seconds. The same VM then
joined in 1.4, 2.0 and 1.9 seconds `[V]` (three runs against the real public
pool). Unit tests (`relaypool_test.go`) pin that sixty slow probes finish in
about the time of one, the ordering and the fall-through to the next relay; the
private-relay integration tests go through it with the real `strelaysrv`
(`TestMeetingThroughAPrivateRelayAndDiscoveryServer`, pool form). A single
`relay://` URL still uses Syncthing's static client as before.
