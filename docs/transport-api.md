<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# `mrcore/transport`: API surface and extraction notes

Prepared for a possible later extraction of `mrcore/transport` into a
package of its own. Nothing here has been applied: the code is unchanged
except for the dependency work described under "Done in this branch".
Markers: `[V]` verified by running the command named, `[I]` intended or
inferred from reading code, `[U]` unknown.

## What the package is

An authenticated byte stream between a waiting party (`Listen`) and a
dialling party (`Dial`), each identified by a Syncthing Device ID
derived from its TLS certificate, found via Syncthing global discovery
plus the public relay pool, via LAN multicast, or via both raced
(`Multi`). It knows nothing about MR-1, LUKS, or any message format `[V]`
(`go list -f '{{.Imports}}'` shows no import of `mrcore` or any
`cmd/` package).

## Done in this branch

- `mrcore/transport` (tests included) no longer imports the root
  package of `syncthing-socket`, which is the whole CLI. It imports only
  `github.com/muelli/syncthing-socket/discovery` (`LookupRecord`,
  `DefaultURL`, `DefaultAnnounceInterval`), a package that uses the
  standard library alone `[V]`: `go list -deps -test ./mrcore/transport`
  shows exactly one `syncthing-socket` package, `discovery`, and
  `go list -deps -test ./...` shows the root package nowhere.
- Behaviour of the discovery lookup is `syncthing-socket`'s, covered by
  its own tests; this package's tests exercise it only through
  `SyncthingRelay` and its `httptest` discovery servers `[V]`
  (`go test ./mrcore/transport`).
- `doc.go` carries the package documentation; `example_test.go` holds
  one runnable `Example` (direct loopback TCP, with an
  `// Output:` check) `[V]`.
- `go.mod` requires `syncthing-socket` at a real pseudo-version with no
  `replace` directive `[V]`. A throwaway module outside the repository,
  with a `replace` onto this worktree for `github.com/muelli/synctang`
  and `GOPROXY=off`, imported `transport` and built `[V]`
  (`go list -m` there reported the same `syncthing-socket` version).
  That works because every module is already in the local module cache;
  whether it resolves from a clean machine against a published module
  is `[U]` (see risk 6).

## Exported API

Frozen names are marked **keep**; candidates for removal from the
exported surface are marked **unexport?**. The proposals are not
applied.

### Interfaces and option structs

| Name | Kind | Used outside the package | Proposal |
|---|---|---|---|
| `Transport` | interface (`Listen`, `Dial`) | `cmd/unlocker`, `cmd/keyholder`, `mobile` | **keep** |
| `Conn` | interface (`PeerID() string` plus `io.ReadWriteCloser`) | `cmd/unlocker`, `mobile` | **keep** |
| `ListenOptions{AuthorizedPeers, DirectAddr}` | struct | `cmd/`, `mobile` | **keep** |
| `DialOptions{PeerID, DirectAddr}` | struct | `cmd/`, `mobile` | **keep** |

`DirectAddr` is documented as a test and pairing aid but is used in
production by `unlocker pair` and `mobile/pair.go`. Keep it, and
describe it as "plain TCP, identity still pinned" rather than as test-only.

### Implementations

| Name | Used outside the package | Proposal |
|---|---|---|
| `SyncthingRelay` and `NewSyncthingRelay(cert)` | yes | **keep** |
| `LocalDiscovery` | yes | **keep** |
| `Multi{Transports, RetryInterval}` | yes | **keep** |
| `LoadOrCreateCert(path)` | yes, every command | **keep**, see "Gaps" |

Fields of the implementation structs, by non-test use outside the
package (a search of `cmd/` and `mobile/` for composite literals and
assignments `[V]`):

| Field | Outside use (non-test) | Proposal |
|---|---|---|
| `SyncthingRelay.Cert`, `.Logger` | yes (`cmd/unlocker/main.go`) | **keep** |
| `LocalDiscovery.Cert`, `.Logger` | yes | **keep** |
| `Multi.Transports` | yes | **keep** |
| `SyncthingRelay.RelayPoolURL`, `.DiscoveryURL`, `.AnnounceURLs` | none | keep as options only if self-hosted infrastructure is a goal; otherwise **unexport?** |
| `SyncthingRelay.AnnounceInterval`, `.AnnounceRefresh`, `.RelayLossGrace` | none (in-package tests only) | **unexport?** (test seams) |
| `LocalDiscovery.GroupAddr`, `.Port` | none | **unexport?** (test seams) |
| `LocalDiscovery.AnnounceInterval` | tests of `cmd/unlocker`, `mobile` (`cmd/unlocker/agent_test.go`, `mobile/local_discovery_test.go`) | **unexport?** if those tests move behind a test-only constructor |
| `Multi.RetryInterval` | none | **unexport?** |

## What to stabilise

1. **The LAN announcement.** Deployed devices already speak it, so these
   are wire-format constants, not tuning knobs: multicast group
   `239.255.42.99`, UDP port `21076`, JSON body
   `{"magic":"synctang-local-discovery-v1","device_id":...,"port":...}`.
   Do not rename or change them in an extraction. (A rename would be a
   flag-day for every installed unlocker and every phone.)
2. **Identity semantics.** A Device ID is Syncthing's encoding of the
   SHA-256 of the certificate DER bytes. `LoadOrCreateCert` persists the
   certificate bytes (PEM certificate then `EC PRIVATE KEY` in one file)
   precisely so the ID never changes. The file format is part of the
   contract.
3. **Handshake rules.** TLS 1.3 minimum, `RequestClientCert` on the
   server, `InsecureSkipVerify` on the client with the Device ID check
   replacing CA verification, authorised-peer check strictly after the
   handshake. These are in `doServerHandshake` and `doClientHandshake`
   `[I]`.
4. **The race and retry behaviour of `Multi`.** Listen restarts a failed
   leg, Dial does not; a late second success is closed, not leaked; each
   attempt has its own context and the winner's is released by its
   `Conn.Close`. `AGENTS.md` records why each of these is load-bearing.
   Existing tests (`TestMultiKeepsRacingAfterALegFails` and the others in
   `multi_test.go`) pin them; none was touched here `[V]` (the diff of
   this branch touches no `multi*.go` file).
5. **`Conn.PeerID()` as a verified value**, not a claim.

## What to unexport (proposal)

- The option fields marked **unexport?** above. Mechanism if done:
  replace the public fields by functional options or an internal
  `testing` constructor, keeping `Cert` and `Logger`.
- Nothing else is exported that should not be. All helpers
  (`doServerHandshake`, `raceRelayAddresses`, `watchRelayLoss`,
  `announceOnce`, and so on) are already unexported
  `[V]` (`go doc -all`).

## Gaps a stand-alone consumer would hit

1. **No exported way to compute a Device ID from a certificate.** The
   `Example` has to import `github.com/syncthing/syncthing/lib/protocol`
   for `NewDeviceID`, and so does every caller. Proposal: add
   `func DeviceID(cert tls.Certificate) string` (and a parser or
   validator for ID strings), so callers need not import Syncthing at
   all. This is an addition, not a trim, so it was not applied.
2. **`Dial` with an empty `PeerID` pins nothing** when `DirectAddr` is
   set: `doClientHandshake` skips the comparison when
   `expectedPeerID == ""`. The relay path rejects an empty or invalid ID
   when it parses it. Proposal: reject an empty `PeerID` up front in
   every implementation, unless an explicit "trust on first use" option
   is added for pairing `[I]`.
3. **`Listen` and `Dial` differ in retry semantics** (listen restarts,
   dial does not); this is documented in `doc.go` but not in the
   `Transport` interface comment.
4. **Logging** uses `log/slog` with a custom trace level
   (`slog.Level(-8)`) that `cmd/unlocker` redefines independently. A
   stand-alone package should export or document the level.

## Dependencies

Measured with `go list -deps` on the package (non-test) `[V]`:

- 303 packages in total: 208 standard library, 93 from third-party
  modules, 1 from `syncthing-socket` (`discovery`), 1 from this
  repository's own module.
- 21 third-party modules (22 with this repository's module), the 20
  below plus `github.com/muelli/syncthing-socket`:

  ```
  github.com/beorn7/perks              github.com/prometheus/client_golang
  github.com/calmh/xdr                 github.com/prometheus/client_model
  github.com/cespare/xxhash/v2         github.com/prometheus/common
  github.com/hashicorp/golang-lru/v2   github.com/prometheus/procfs
  github.com/miscreant/miscreant.go    github.com/shirou/gopsutil/v4
  github.com/munnerz/goautoneg         github.com/syncthing/notify
  github.com/pierrec/lz4/v4            github.com/syncthing/syncthing
  github.com/thejerf/suture/v4         golang.org/x/crypto
  golang.org/x/net                     golang.org/x/sys
  golang.org/x/text                    google.golang.org/protobuf
  ```

- Three direct third-party imports: Syncthing's `lib/protocol`
  (`NewDeviceID`, `DeviceIDFromString`, `DeviceID`) and
  `lib/relay/client` (`NewClient`, `GetInvitationFromRelay`,
  `JoinSession`), and `syncthing-socket/discovery`. Everything else in
  the list above is pulled in by the two Syncthing packages (protobuf,
  prometheus, lz4, xdr, suture, and so on); `discovery` adds nothing.
- `github.com/syncthing/syncthing` is licensed MPL-2.0 `[V]` (first lines
  of its `LICENSE` in the module cache). Whether an AGPL-3.0-or-later
  package may link it without further steps follows what
  `THIRD_PARTY.md` already records for this repository; a split would
  need to carry that entry over `[U]` (not re-examined here).
- `syncthing-socket` is in the package's graph only as `discovery`
  `[V]`, and it is AGPL-3.0-or-later like this repository `[V]` (every Go
  file in that module carries the SPDX header); `mrcore` itself needs only `filippo.io/nistec` and the
  standard library `[V]` (`go list -deps ./mrcore`).

Cheapest ways to cut the count, none applied: add `DeviceID`/validation
helpers so `lib/protocol` is only an internal detail; vendor or
reimplement the roughly four `lib/relay/client` calls against the relay
protocol (high effort, `[U]` how much of `lib/relay/protocol` it would
drag in).

## Stability risks

1. **Syncthing library internals.** `lib/relay/client` is not a public,
   versioned API; this package is pinned to v1.30.0. A bump may change
   `client.NewClient`, `GetInvitationFromRelay`, or the dynamic-pool
   behaviour that `AGENTS.md` documents (walk once, then give up).
   Mitigation: keep the pin, keep the `Multi` retry, run the soak.
2. **Public infrastructure.** Discovery servers and the relay pool are
   shared and not ours; their rate limits, certificates, URLs
   (`discovery-lookup`, `discovery-announce-v4/v6`,
   `relays.syncthing.net`) and record-merging behaviour are outside our
   control. The tests use `httptest` servers and in-process doubles, so
   **none of the live behaviour was exercised in this branch** `[U]`.
3. **Global discovery merges announcements**, so records accumulate
   stale relay addresses; dialling must race the whole list (done in
   `raceRelayAddresses`). A consumer reimplementing dial must keep this.
4. **Relay session lifetime.** The relay drops a device's sessions when
   its control connection drops; never cancel a winning attempt's
   context. This is an invariant of the API, not only of the code, and
   belongs in `Conn.Close` documentation if the package is split.
5. **Local discovery is IPv4 only, unauthenticated, and needs a bound
   interface address** to send without a default route. The address in an
   announcement comes from the datagram source, never the payload.
6. **Module path.** The import path is
   `github.com/muelli/synctang/mrcore/transport`. A split changes it and
   every importer. This repository's `go.mod` no longer carries a
   `replace` directive, and `syncthing-socket` is required at a real
   pseudo-version, so a module outside this repository can require it
   without inheriting a broken graph `[V]` (offline experiment above).
   `syncthing-socket`'s release tags are plain integers (`v164`), which
   Go cannot query, so consumers pin it by commit.
7. **`LoadOrCreateCert` writes a private key file** (mode `0600`,
   directory `0700`) and treats an existing file as authoritative; there
   is no rotation or revocation.
