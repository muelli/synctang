<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Your own relay and discovery server

The public Syncthing relay pool and discovery servers are the default, and
nothing changes for anyone who does not set the options below. This note is
for an owner who would rather run their own, and for whoever has to debug
one. `[V]` marks what was run in this branch, `[I]` what is inferred from
reading code, `[U]` what is unknown or not yet tried.

## What a meeting needs

The machine listens. It joins one relay and announces that relay's address,
under its Device ID, to a discovery server. The key holder dials: it looks
the machine's Device ID up on a discovery server, is handed the relay
address (or a direct one), and joins the same relay `[V]` (read in
`mrcore/transport/syncthing_relay.go`, and shown by the integration test
below: a key holder given only the discovery server reached a machine that
had been given a relay and the same discovery server).

So the two sides must agree on the **discovery server**, and only the machine
needs the **relay** setting. A key holder given a relay pool does not use
it; the option exists so one config file can serve both.

## The settings

Three keys, all optional, each a URL:

| Key | Meaning |
| --- | --- |
| `relay-pool` | An `http(s)` URL of a relay pool, or `relay://host:port/?id=<relay Device ID>` for one relay, which is then the only relay used. |
| `discovery` | The discovery server, used to look a machine up and, unless `discovery-announce` is set, to announce to. |
| `discovery-announce` | Where announcements are posted, if that differs from `discovery`. |

Both forms of `relay-pool` are accepted `[V]` (the library's relay client
takes `relay://` for one relay and `dynamic+http(s)://` for a pool, and the
tool adds the `dynamic+` itself). `relay://` needs the relay's Device ID,
which `strelaysrv` prints at start-up as its `URI:` line, so copy the whole
line. The library checks the relay's certificate against it and, on a
mismatch, fails with "relay id does not match" `[I]` (read in `lib/relay/client`,
not run). Which form was understood is printed at start: `unlocker: using single
relay 192.0.2.10:22067, discovery disco.example.org`.

A wrong value is an error that stops the command and names the setting. It
never falls back to the public servers: that would announce, on the servers
the owner chose not to use, a machine the owner chose not to announce there.
For the same reason a `discovery` with no `discovery-announce` is announced
to as well as looked up on; the public announce servers are not left in
place `[V]` (unit test `TestNetworkConfigPrivateDiscoveryNeverAnnouncesPublicly`).

## Where they are set

- **Machine:** `/etc/unlocker/network.conf`, one `key=value` per line, `#`
  comments. Read by `unlocker agent`, `pair` and `open` (`--network-conf`
  names another file). The dracut module copies it into the initrd when it
  exists, so after changing it rebuild the initrd (for example
  `sudo dracut --force`). `[V]` that the module's helper finds the file and
  skips a missing or dangling one (`scripts/test-network-conf.sh`); `[U]`
  that a real initrd build carries it and the agent reads it at boot.
- **Kernel command line**, which wins over the file: `rd.unlocker.relay=URL`
  and `rd.unlocker.discovery=URL`. The agent reads `/proc/cmdline` itself, so
  nothing is installed for this. It is the setting a person can change at the
  boot prompt when the network path is what is broken, as `rd.unlocker=0` is
  for switching the agent off. A discovery server given here replaces the
  file's discovery server and its announce endpoint together `[V]`.
- **Laptop:** `keyholder unlock --relay-pool URL --discovery URL
  [--discovery-announce URL]`, or the same keys in `~/.config/keyholder/config`
  (the user config directory, so `$XDG_CONFIG_HOME/keyholder/config` if set;
  `--config` names another file). Flags win over the file.
- **Phone:** from the pairing link, below. There is no field to type a URL
  into in this version.

## The pairing link

`unlocker pair` adds `relay=` and `discovery=` (URL-escaped) to the
`synctang://pair?...` link and its QR code when the machine has them set, and
nothing when it has not, so the link for the public servers is unchanged
`[V]` (unit test). The announce endpoint is not carried: a phone only looks
up.

The app stores both with the machine and passes them to every later unlock
`[V]` for the parsing and the hand-over to the Go side (JVM unit tests and Go
tests); `[U]` on the phone itself, which was not touched.

**A link with a parameter the app does not know is refused**, with a message
naming it, and nothing is paired. Ignoring it could pair the phone against
servers the owner chose not to use. So are `relay` and `discovery` values
that are not server URLs `[V]`. The reverse does not hold: a phone running
an app from before this change ignores `relay=` and `discovery=`, looks the
machine up on the public servers, and does not find it `[I]` (read from the
old parser). Update the app first.

The link is longer, so the QR code is denser. `[U]` whether a Pixel's camera
reads it at the usual screen size; the plan was to measure that before
settling the field names. If it does not, the printed link can be pasted.

## What the servers see

The relay sees two Device IDs and the volume of traffic between them; the
tunnel is TLS 1.3 with the peer pinned by Device ID, so it cannot read or
alter an unlock exchange. The discovery server sees the machine's Device ID,
the relay address it announced, and the network addresses that announce and
look it up `[I]` (read in `stdiscosrv`'s `apisrv.go`). Running them yourself
moves that knowledge to you; it does not reduce it, and it does not hide the
fact that a machine is waiting from anyone who can see its traffic.

## Constraints found by running it

- **`stdiscosrv` drops loopback addresses** from what it is told
  (`fixupAddresses` in its `apisrv.go`), so a relay announced as
  `relay://127.0.0.1:...` is never stored and the machine is never found, with
  nothing but a "no addresses announced" on the dialling side `[V]`. Use the
  relay's LAN address.
- **TLS to the discovery server and a pool URL is checked against the system
  trust store** (the initrd carries the machine's bundle). A server whose
  certificate that store does not trust fails the lookup. A private CA is out
  of scope for now, and on the phone `[I]` there is no place to add one. Plain
  `http://` is accepted for a server on a private network; what discovery
  returns is only a hint about where to dial, and the identity is checked by
  Device ID afterwards, so the cost is that the lookup is readable on the
  wire. Announcements authenticate by the announcer's TLS client certificate,
  and the announce client does not verify the server's certificate, as
  before.
- The relay URL in a record is the one the relay is configured to advertise;
  behind NAT use `strelaysrv -ext-address` `[I]`.

## Running it for real

Nothing here replaces the test in the plan: a machine VM and a key holder on
a private bridge with no route to the Internet, an unlock that succeeds with
the settings and fails, with the failure named, without them, and the same
with a phone on the LAN. `[U]` None of that was done; what was run is below.

## What was run

All in this branch, on one host, against the real `strelaysrv` and
`stdiscosrv` v1.30.0 (the version the module pins), built outside the module
as the comment at the top of `integration/private_servers_test.go` says:

- `[V]` A machine given a single relay and a discovery server, and a key
  holder given only the discovery server, meet and exchange bytes both ways,
  each seeing the other's Device ID. The same through a relay pool (a small
  HTTP endpoint listing the relay).
- `[V]` A key holder on a different discovery server than the machine's does
  not find it, and the error says no addresses were announced for the peer.
- `[V]` With the machine's announce to the private server removed, the first
  test fails (a mutation check).
- `[U]` The host was not cut off from the Internet while this ran, so "never
  contacts the public servers" is argued from the settings, not enforced.
  The machine VM, the real initrd and the phone were not involved.
