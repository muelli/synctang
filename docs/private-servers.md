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
needs a **relay** setting. A dialling side (key holder, phone) has none: it
learns the relay from discovery. A relay-only setup, with a static relay
address and no discovery server, is possible in principle and parked; it is
not built.

## The settings

Three keys, all optional, each a URL. On a key holder only `discovery` and
`discovery-announce` exist: a `relay-pool` line in its config file is refused,
saying it is a machine setting `[V]` (`TestKeyholderConfigRefusesRelayPool`),
and there is no `--relay-pool` flag (`TestRunUnlockHasNoRelayPoolFlag`).

| Key | Meaning |
| --- | --- |
| `relay-pool` | An `http(s)` URL of a relay pool, or `relay://host:port/?id=<relay Device ID>` for one relay, which is then the only relay used. |
| `discovery` | The discovery server, used to look a machine up and, unless `discovery-announce` is set, to announce to. May say how to trust the server, see below. |
| `discovery-announce` | Where announcements are posted, if that differs from `discovery`. Always `https`. |

Both forms of `relay-pool` are accepted `[V]` (the library's relay client
takes `relay://` for one relay and `dynamic+http(s)://` for a pool, and the
tool adds the `dynamic+` itself). `relay://` needs the relay's Device ID,
which `strelaysrv` prints at start-up as its `URI:` line, so copy the whole
line. The library checks the relay's certificate against it and, on a
mismatch, fails with "relay id does not match" `[I]` (read in `lib/relay/client`,
not run). Which form was understood is printed at start: `synctang-unlocker: using single
relay 192.0.2.10:22067, discovery disco.example.org`.

A wrong value is an error that stops the command and names the setting. It
never falls back to the public servers: that would announce, on the servers
the owner chose not to use, a machine the owner chose not to announce there.
For the same reason a `discovery` with no `discovery-announce` is announced
to as well as looked up on; the public announce servers are not left in
place `[V]` (unit test `TestNetworkConfigPrivateDiscoveryNeverAnnouncesPublicly`).

## Trusting a private discovery server

A private discovery server usually has a self-signed certificate, which no
certificate authority vouches for. The `discovery` URL says how to trust it,
in Syncthing's own syntax (`lib/discover/global.go` in v1.30.0):

| In the URL | Meaning |
| --- | --- |
| `?id=<Device ID>` | The server's certificate must be exactly that Device ID, the hash Syncthing derives from a certificate. `stdiscosrv` logs it at start-up as "Server device ID is ..." `[I]` (read in its `main.go`; the integration test computes it from the certificate file). No certificate authority is involved, so a self-signed one is fine. |
| `?insecure` | Do not verify the server's certificate. Anyone on the path can then answer lookups. What they return is only a hint, because the machine is checked by its Device ID afterwards, so the cost is availability and a readable lookup, not a machine impersonated. |
| neither | Verify against the system's certificate authorities, as before. A self-signed server fails. |

Example: `discovery=https://192.0.2.20:8443/v2/?id=<the server's Device ID>`.
`?id=` and `?insecure` are for the client and are taken out of the URL before
anything is sent, as Syncthing does; any other query is kept. An id is checked
on every connection, before the request is sent, so a wrong one is refused
with an error naming both ids ("id mismatch") and the lookup is never made
to that server. (Syncthing's own client checks after the response has come
back `[I]`; this one checks during the TLS handshake, which also runs for a
resumed session.)

Plain `http://` is for looking up only, never for announcing: the setting a
machine announces to must be `https`, and `synctang-unlocker` says so and stops when it
is not. A dialling side (laptop, phone) may use `http://...?insecure` for its
`discovery`. A machine may give a plain-`http` `discovery` (which is what its
pairing link hands the phone) only together with an `https`
`discovery-announce`. Announcements are made with the machine's own
certificate as the client identity. With `?id=` on the announce URL (which is
the `discovery` URL when no `discovery-announce` is given) the announcement is
pinned exactly like a lookup: it goes only to the server with that Device ID,
and nothing is posted to any other. With `?insecure`, or with neither, the
server's certificate is not verified, as before.

What is verified, with the tests that show it:

- `[V]` An `?id=` that matches a self-signed TLS server is accepted, and the
  record's `Seen` timestamp is still returned: `TestLookupPinnedByDeviceIDAcceptsASelfSignedServer`
  (`mrcore/transport`, httptest TLS server).
- `[V]` A wrong id is refused naming the mismatch and both ids, and the server
  receives no request: `TestLookupPinnedToTheWrongDeviceIDIsRefusedBeforeAnythingIsSent`,
  and through `Dial`: `TestDialRefusesADiscoveryServerWithTheWrongID`.
  Mutation check: with the comparison disabled these tests and the integration
  test below fail.
- `[V]` `?insecure` is accepted, `?insecure=false` is not (`TestLookupInsecureAcceptsASelfSignedServer`);
  neither id nor insecure refuses a self-signed server, no request sent
  (`TestLookupWithNeitherUsesTheSystemCAs`).
- `[V]` `id` and `insecure` are not sent to the server and other query
  parameters are (`TestLookupSendsTheDeviceAndNotTheTrustOptions`); likewise on
  an announce URL (`TestAnnounceDoesNotSendTheTrustOptions`).
- `[V]` An announcement pinned by `?id=` is posted to a self-signed server with
  that id (`TestAnnouncePinnedByDeviceIDPostsToTheRightServer`) and to no other:
  with a wrong id it fails naming the id mismatch and nothing is posted, even if
  an HTTP client that trusts that server is available
  (`TestAnnouncePinnedToTheWrongDeviceIDPostsNothing`). `?insecure` and no option
  stay unpinned (`TestAnnounceInsecureAndPlainStayUnpinned`). Against the real
  `stdiscosrv`, a machine given a wrong id announces nothing and the key holder
  does not find it (`TestAMachinePinnedToTheWrongServerIDAnnouncesNothing`); the
  other integration tests announce with the right id and are found. Mutation
  check: with the pinned client disabled, the wrong-id unit test and the
  integration test fail.
- `[V]` Nothing is announced over plain http (`TestAnnounceRefusesPlainHTTP`);
  the machine's settings refuse an http `discovery` that it would announce to and
  an http `discovery-announce` (`TestMachineDiscoveryRefusesPlainHTTP`); a
  dialling side accepts `http` only with `?insecure`, and `?id=` only with https
  and a well-formed Device ID (`TestDialerDiscoveryMayBePlainHTTPWithInsecure`,
  `TestNetworkConfigDiscoveryIDIsValidated`).
- `[V]` Against the real `stdiscosrv` v1.30.0, with the certificate it makes for
  itself (`integration/private_servers_test.go`): a machine and a key holder
  both given `?id=<its Device ID>` meet; a key holder given `?insecure` meets;
  a key holder given a wrong id never connects and the error names the mismatch
  (`TestAKeyHolderPinnedToTheWrongServerIDNeverConnects`); a key holder given
  neither is refused with a certificate error
  (`TestAKeyHolderWithNeitherIDNorInsecureRefusesASelfSignedServer`).
- `[U]` The phone: its Go library is this code and the app passes it the link's
  `discovery=` (JVM tests: `aLinkCanCarryTheMachinesDiscoveryServer`), but no
  lookup was made from a phone, and the QR code carrying an `?id=` URL (longer)
  was not scanned.

## Where they are set

- **Machine:** `/etc/synctang/network.conf`, one `key=value` per line, `#`
  comments. Read by `synctang-unlocker agent`, `pair` and `open` (`--network-conf`
  names another file). The dracut module copies it into the initrd when it
  exists, so after changing it rebuild the initrd (for example
  `sudo dracut --force`). `[V]` that the module's helper finds the file and
  skips a missing or dangling one (`scripts/test-network-conf.sh`). `[V]` on a
  real initrd (Fedora 44, dracut 108, 2026-11-02): `dracut --force` put
  `etc/synctang/network.conf` into the initrd (`lsinitrd`), and at boot the agent
  printed `using relay public, discovery discovery.invalid.example`, an unreachable
  server named in the file, after which a laptop key holder on the public servers
  could not find the machine (its unlock timed out). With the kernel argument
  `rd.synctang.discovery=` naming the public announce endpoint added, the agent
  printed that server instead and the same laptop unlocked the machine, so the
  command line beats the file in a real initrd too.
- **Kernel command line**, which wins over the file: `rd.synctang.relay=URL`
  and `rd.synctang.discovery=URL`. The agent reads `/proc/cmdline` itself, so
  nothing is installed for this. It is the setting a person can change at the
  boot prompt when the network path is what is broken, as `rd.synctang=0` is
  for switching the agent off. A discovery server given here replaces the
  file's discovery server and its announce endpoint together `[V]`.
- **Laptop:** `synctang-keyholder unlock --discovery URL [--discovery-announce URL]`, or
  the same keys in `~/.config/synctang-keyholder/config`
  (the user config directory, so `$XDG_CONFIG_HOME/synctang-keyholder/config` if set;
  `--config` names another file). Flags win over the file.
- **Phone:** from the pairing link, below. There is no field to type a URL
  into in this version.

## The pairing link

`synctang-unlocker pair` adds `discovery=` (URL-escaped) to the `synctang://pair?...`
link and its QR code when the machine has one set, and nothing when it has
not, so the link for the public servers is unchanged `[V]`
(`TestPairingURLCarriesTheNetworkSettings`). Neither the relay nor the announce
endpoint is carried: only the machine joins a relay or announces, and a phone
only looks up.

The app stores the discovery server with the machine and passes it to every later unlock
`[V]` for the parsing and the hand-over to the Go side (JVM unit tests and Go
tests); `[U]` on the phone itself, which was not touched.

**A link with a parameter the app does not know is refused**, with a message
naming it, and nothing is paired. Ignoring it could pair the phone against
servers the owner chose not to use. That includes a link that carries
`relay=`: the message names `relay` `[V]` (`aLinkWithARelaySettingIsRefusedAsAnUnknownParameter`,
`aLinkWithARelaySettingIsRefusedAndSaysWhich`, JVM tests). A `discovery` value
that is not a server URL is refused too `[V]`, and so is one over plain
`http` without `?insecure`, with its own message `[V]`
(`aDiscoveryServerOverPlainHttpNeedsInsecure`, `aLinkWithAnHttpDiscoveryServerWithoutInsecureIsRefusedAndSaysSo`;
the Go side still refuses it too, at unlock time).
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
- **A pool URL is fetched with the system trust store** (the initrd carries
  the machine's bundle), by the library's own client, which has no option for
  anything else. A relay pool whose certificate that store does not trust
  fails `[I]` (read in `lib/relay/client/dynamic.go`, not run). The relay
  itself is checked by its Device ID, from the `?id=` of a `relay://` URL or
  from the pool's list, not by a certificate authority.
- The relay URL in a record is the one the relay is configured to advertise;
  behind NAT use `strelaysrv -ext-address` `[I]`.

## Running it for real

Nothing here replaces the test in the plan: a machine VM and a key holder on
a private bridge with no route to the Internet, an unlock that succeeds with
the settings and fails, with the failure named, without them, and the same
with a phone on the LAN. `[U]` None of that was done; what was run is below.

## What was run

All in this branch, on one host. The integration tests use the real
`strelaysrv` and `stdiscosrv` v1.30.0 (the version the module pins), built
outside the module as the comment at the top of
`integration/private_servers_test.go` says; the unit tests are in
`mrcore/transport`, `mobile`, `cmd/synctang-keyholder`, `cmd/synctang-unlocker` and the Android
app's JVM tests.

- `[V]` A machine given a single relay and a pinned discovery server, and a
  key holder given only the discovery server (pinned, or `?insecure`), meet
  and exchange bytes both ways, each seeing the other's Device ID; the same
  through a relay pool (a small HTTP endpoint listing the relay):
  `TestMeetingThroughAPrivateRelayAndDiscoveryServer`.
- `[V]` A key holder on a different discovery server than the machine's does
  not find it, and the error says no addresses were announced for the peer
  (`TestAKeyHolderOnAnotherDiscoveryServerDoesNotFindTheMachine`).
- `[V]` With the machine's announce to the private server removed, the first
  test fails; with the id comparison disabled, the wrong-id tests fail; with the
  pinned announce client disabled, the wrong-id announce tests fail (mutation
  checks).
- `[V]` On a real private relay the first session after an announcement
  sometimes failed to complete (a debug log of one such run: the relay logged
  "Failed to add ... to session" and the machine's control connection closed),
  and the meeting only succeeded after the machine listened again, which is
  what the agent does. Some meetings in the integration runs took 16 to 29
  seconds for that reason. Not diagnosed further `[U]`; it is why the tests
  listen again.
- `[V]` On an isolated network (2026-11-03): a Fedora 44 VM with a LUKS2 root
  on a bridge that has an address on the host and no route anywhere else
  (`scripts/test-vm-net.sh`), the built programs under their new names, the
  initrd built by `dracut --force` with the renamed module and a
  `/etc/synctang/network.conf` naming a private relay (`relay://10.77.0.1:22067/?id=...`)
  and a private discovery server (`https://10.77.0.1:8443/v2/?id=...`, both on the
  host). From the VM, `curl https://relays.syncthing.net/endpoint` failed (no HTTP
  status) and `ping 1.1.1.1` lost every packet, while the private discovery
  server answered. A laptop key holder on the host
  (`synctang-keyholder unlock --discovery https://10.77.0.1:8443/v2/?id=...`)
  unlocked the VM at its LUKS prompt, with nobody typing a passphrase, twice; the
  agent printed `using single relay 10.77.0.1:22067, discovery 10.77.0.1:8443`.
  A packet capture of the VM's frames over a whole reboot and unlock (163
  frames) shows, besides DHCP, ARP, mDNS, IPv6 and the local-discovery
  multicast (`239.255.42.99`), only packets to `10.77.0.1`, 74 of them, and NTP
  (UDP 123) to four public addresses from the operating system's own `chronyd`,
  which started about 15 seconds after the unlock had finished. Nothing from
  the agent went anywhere else.
- `[U]` The phone was not on that network, and the dialling side's lookups
  on a private server were tried only from a laptop key holder, not from the app.
