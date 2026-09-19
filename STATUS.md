# Status

Updated at the end of every work session. This file is the primary channel
for following progress; assume no other channel is read.

## 2026-09-18, session 1

### Done

- Repository scaffolding: `LICENSE` (AGPL-3.0 full text), `debian/copyright`,
  SPDX header convention, `go.mod` (module `github.com/muelli/synctang`).
- CI skeleton: GitHub Actions with separate jobs for `go test`, a privileged
  loopback-LUKS job, shellcheck, the em-dash check, and an Android job (a
  no-op until the Android module exists).
- `justfile` with `just test` (non-privileged) and `just test-go-privileged`.
- `THIRD_PARTY.md` started: `syncthing-socket` checked, licensed AGPL-3.0,
  compatible by combination with our AGPL-3.0-or-later.
- Access confirmed: push to `github.com/muelli/synctang` over SSH deploy key;
  SSH to the test VM (`ubuntu2604-evo.fritz.box`, Ubuntu 26.04.1 LTS,
  `dracut` 110-11ubuntu0.1, `cryptsetup` installed at 2.8.4). `cryptsetup`
  was missing on both this host and the VM; installed on both.

- WP1 (`mrcore` math) complete: T1.1 to T1.5 all green.
  - `mrcore.Group` interface plus a P-256 implementation on top of
    `filippo.io/nistec`'s exported API only (Add, ScalarMult, ScalarBaseMult,
    SetBytes, BytesX). Point negation is a `big.Int` subtraction on the raw
    y-coordinate (field prime from `crypto/elliptic.P256().Params().P`);
    x-only point lifting reuses nistec's compressed-point decoding
    (0x02/0x03 prefix), so no unexported nistec internals were needed and
    nothing had to be forked or vendored.
  - `Enrol`/`ChallengeStart`/`FinishFullPoint` (T1.1, full-point key holder:
    file backend, Tang-style) and `FinishXOnly` (T1.2, x-only key holder:
    Android Keystore/TPM2 ECDH, tries both sign candidates from `LiftX`).
    `TestRecoverWrongKeyFails` (T1.3) checks both paths return an error, not
    a panic, on a wrong key holder.
  - AES-256-GCM and HKDF-SHA256 from the standard library (`crypto/aes`,
    `crypto/cipher`, `crypto/hkdf`, the last new in Go 1.24+): no new
    third-party dependency beyond `nistec` for the whole crypto core.
  - `Token` (T1.4): `type`/`version` checked explicitly on decode, unknown
    top-level fields preserved through an unexported `extra` map.
  - `testdata/mr1.json` (T1.5): fixed vectors for a full enrol/recover
    worked example, generated once with the (already-tested) implementation
    and self-checked before committing; for WP5's Kotlin instrumented tests
    to verify an independent implementation against the same numbers.

- WP0 (Android Keystore ECDH spike) emulator phase complete: `docs/wp0-keystore-ecdh.md`.
  Core assertion PASSED on an API 34 emulator (non-StrongBox backend):
  Keystore ECDH accepted a manually-constructed P-256 peer point
  (`ECPublicKeySpec` from raw coordinates, never touched by a
  `KeyPairGenerator`) and returned the raw 32-byte x-coordinate, matching
  an independent software ECDH computation exactly. `StrongBoxUnavailableException`
  thrown as expected (no StrongBox hardware on the emulator); fell back to
  a non-StrongBox key and still passed. This confirms the curve choice
  (P-256) is workable for the Keystore ECDH path in principle; StrongBox
  itself and biometric gating still need a real device (see "Blocked").

- WP6 (dracut module) skeleton complete: `dracut/90unlocker/{module-setup,
  unlocker-start,unlocker-stop}.sh`, copied and adapted from
  `syncthing-socket`'s `contrib/dracut-luks/90syncthing-socket/` (renamed
  binary/PID file/kernel flag `rd.unlocker=0`, dropped the
  `/etc/*/luks.conf` override since this project's only configuration
  source is the LUKS2 mr-1 token itself). `just install-dracut` installs
  the whole module directory. `unlocker enrol` rebuilding the initrd and
  verifying it with `lsinitrd` (the other half of WP6) waits on `enrol`
  existing, which it now does; not done yet, WP7-adjacent, to pick up
  after the current delegated task lands.

- WP4 (`keyholder`) file backend complete: `init`/`export-pubkey`/`unlock`.
  `s` (MR-1 scalar) and a separate persistent transport identity (PEM,
  `transport.LoadOrCreateCert`) are two different files, deliberately not
  combined: different key types for different protocols, and either can
  be rotated independently. `export-pubkey` prints both `S` and the
  transport ID, for pasting into `unlocker enrol --pubkey
  --recipient-transport-id`. `unlock <machine-transport-id>` dials, shows
  the machine's name (`mrcore.Hello`) and waits for Enter or `--yes`,
  relays a confirm code if the machine asks for one, answers once with
  the full point `Y = s.X` (the file backend always can; only a
  hardware-backed key holder is limited to `XOnly`), exits.
  `pair` (live QR/network pairing) and the TPM2 backend behind a build
  tag not started; TPM2 remains an open `[U]` item.

- WP1 extended after real cryptsetup testing surfaced a gap: `Token` gained
  a `Keyslots []string` field. LUKS2 requires every on-disk token to carry
  a `keyslots` array, even empty; `cryptsetup token import` rejects a
  document without one with a bare "Failed to import token from file.", no
  further detail. `MarshalJSON` always emits it now (default `[]`);
  decode stays lenient. Verified end to end against real cryptsetup 2.8.4
  on a loopback image.
- WP1 gained `mrcore/wire.go`: `RecoverRequest`/`RecoverResponse` (the
  recovery exchange), `Hello` (machine's name, shown before a key holder
  approves, plan section 5), `ConfirmCodeChallenge`/`ConfirmCodeResponse`
  (T3.3), and `WriteMessage`/`ReadMessage` (4-byte length prefix + JSON,
  64KB bound). Shared here rather than duplicated in `cmd/unlocker` and
  `cmd/keyholder` so both binaries cannot drift apart on the wire format.

- WP2 (transport interface + syncthing relay implementation) complete:
  T2.1, T2.2 green. `mrcore/transport.Transport`/`Conn` interfaces;
  `SyncthingRelay` implements both the offline `DirectAddr` path (tested)
  and the real relay+discovery path (not exercised by any test, no network
  access in CI). Peer authorization checked strictly after the TLS 1.3
  handshake, against the certificate-derived Syncthing Device ID, never
  against anything a dialer merely claims first.
  - Real building blocks used directly (all exported, public API, nothing
    unexported copied): `github.com/syncthing/syncthing/lib/relay/client`
    and `.../lib/protocol` for the relay/Device ID mechanics;
    `github.com/muelli/syncthing-socket` (imported as bare module name
    `syncthing-socket`, see the go.mod `replace` line, its own go.mod
    declares that bare path, so a plain `go get github.com/...` fails
    with a module-path-mismatch error; documented so nobody rediscovers
    this the hard way) for `GenerateDeterministicCert`/`LookupRecord`/
    `DefaultDiscoveryURL`.
  - `[U]` **Dependency footprint trade-off worth revisiting**: importing
    `syncthing-socket`'s root package pulls in that package's *entire*
    monolithic file set (shell, SOCKS, PTY, qrterminal, image dithering,
    Windows service wrapper) unconditionally, since Go links a whole
    package once any exported symbol from it is used, regardless of which
    few functions are actually called. That drags in `pion/webrtc`,
    `hashicorp/yamux`, `spf13/cobra`, `kardianos/service`, and roughly 60
    more transitive modules into `unlocker`'s dependency graph and binary,
    even though none of that code ever runs. This sits in real tension
    with the design's own stated advantage over Tang/Clevis ("one static
    Go binary" vs several boot-time dependencies, plan section 6). A
    cheaper alternative for a later pass: copy just
    `GenerateDeterministicCert` (about 30 lines, cert.go in the reference
    repo) into this project instead of importing the whole package, and
    depend on `syncthing/syncthing/lib/relay/client` and `.../lib/protocol`
    directly (already the case) without going through `syncthing-socket`
    at all. Not changed now: WP2 already works and is tested; flagging for
    a deliberate future decision rather than unpicking working, tested
    code.

- WP3 (`unlocker`) complete except `status` and `pair`: `enrol` (T3.1),
  `agent` (T3.2: recovers over the transport, verifies the recovered
  secret with `cryptsetup open --test-passphrase` before ever answering
  systemd, then answers the matching ask-password request; T3.3: sends
  `ConfirmCodeChallenge` right after `Hello`, rejects a wrong or missing
  response before ever sending a `RecoverRequest`). `rd.unlocker=0` was
  already fully handled at the dracut shell level (WP6).
  `--device` auto-discovers from `/proc/partitions` (scanning for a
  device carrying an `mr-1` token) when not given, re-checked on every
  retry: the real dracut hook (`unlocker-start.sh`) has no way to name
  a device, since `systemd-cryptsetup` sets no environment variable for
  it the way `initramfs-tools`' askpass could rely on. Added after the
  delegated agent/unlock work landed and flagged it as a real gap that
  would have made the boot path exit 2 immediately.
  `status` and `pair` (live QR/network pairing, both sides) not started;
  the laptop pairing path is already complete without live pairing
  (`keyholder export-pubkey` + `unlocker enrol --pubkey
  --recipient-transport-id`), and phone pairing needs the Android app
  (WP5) to exist first anyway.
  Wire-protocol note: `mrcore.ConfirmCodeChallenge` has no discriminator
  field, so machine and key holder agree on message order instead: the
  machine always sends it right after `Hello`, with an empty `Code`
  meaning "not required", and the key holder skips its own prompt when
  it sees that.

- `[V]` **Loopback LUKS2 tests do not need root or a privileged CI job**,
  contrary to the plan's assumption (section 7, WP3: "needs root or a CI
  job that has it"). Verified empirically against real cryptsetup 2.8.4
  on this host (uid 1000, `dm_crypt` module not even loaded):
  `luksFormat`, `luksAddKey`, `token import`/`export`, and
  `open --test-passphrase` all succeed unprivileged, because none of them
  map or decrypt the device; only actually opening/mapping it
  (`luksOpen` without `--test-passphrase`) needs `CAP_SYS_ADMIN` and
  `dm_crypt`. `cmd/unlocker`'s tests run in the ordinary `go-test` CI job
  as a result; the `test-go-privileged` job in `.github/workflows/ci.yml`
  is kept for whatever, if anything, later turns out to genuinely need
  root (a true `luksOpen`, say), not removed pre-emptively.
- Also found: `cryptsetup luksAddKey` can take *both* the existing
  passphrase and the new key from the same stdin stream in one
  invocation (`--key-file=- --keyfile-size=N --new-keyfile=-
  --new-keyfile-size=M`, concatenated, read in that order), so the
  generated volume secret P never touches a temp file. Not obvious from
  `--help` alone; found by testing directly. `cryptsetup token import`
  also silently rejects a token whose `keyslots` array names a keyslot ID
  that does not actually exist on the device yet (order matters: add the
  keyslot first, only then write a token referencing it).

- `[V]` A code review pass over `mrcore` (four dimensions: line-by-line
  correctness, cross-file caller/callee, cleanup, altitude) found and
  fixed two real issues, both now merged:
  - `cmd/keyholder/keystore.go`'s `loadPrivateKey`/`publicKeyHex` never
    wiped the private scalar `s` (or the raw hex file bytes it was
    decoded from) after use, unlike `runInit`'s equivalent path, which
    already did. `export-pubkey` left the long-term key sitting in
    process memory for the rest of the process's life.
  - `mrcore.aeadOpen` (reached from `FinishFullPoint`/`FinishXOnly` via
    `finishWithY`) passed `rec.Nonce` straight to `cipher.AEAD.Open`
    without checking its length first; `crypto/cipher`'s GCM
    implementation *panics*, rather than erroring, on a nonce that is
    not exactly 12 bytes. `rec.Nonce` comes from a `Recipient` decoded
    from an on-disk LUKS2 token, so a single corrupted or hand-edited
    byte was a denial of service against recovery, directly
    contradicting `finishWithY`'s own documented "never a panic"
    guarantee. Reproduced with a failing test first, then fixed with an
    explicit length check.
  - Several further findings (a `Token.MarshalJSON`/`UnmarshalJSON`
    refactor to a single shadow struct, `Group.Negate` reimplemented as
    `ScalarMult` by `N-1` instead of hand-rolled `big.Int` arithmetic,
    factoring the duplicated `eS` derivation out of `FinishFullPoint`/
    `FinishXOnly`, `slices.Contains` instead of a hand-rolled
    `containsDeviceID`, caching the discovery HTTP client instead of
    building one per announce tick) are real simplifications, not bugs;
    not applied yet, `[U]` whether to do so before or after WP7.

### In progress

- WP5 (Android app): `gomobile bind` of `mrcore`, Kotlin UI (QR pairing,
  `BiometricPrompt`-gated Keystore ECDH, one Unlock action), F-Droid
  infrastructure adapted from `syncthing-socket`'s existing Android app;
  delegated, running, no result yet as of this line.
- `unlocker status` and both `pair` subcommands: not started.

### Blocked

- Real-device StrongBox + BiometricPrompt retest for WP0 (stop condition in
  the task brief): exact steps are in `docs/wp0-keystore-ecdh.md` under
  "For the human". Needs a StrongBox-capable phone with USB debugging and a
  fingerprint/face already enrolled. Not blocking WP2 onward, since the
  curve and the basic Keystore ECDH mechanism are already confirmed on the
  emulator; only the StrongBox-specific and biometric-specific behaviour
  remain open.

### Decisions taken

- Repo-local git identity set to `Tobias Mueller <muelli@cryptobitch.de>`,
  distinct from the global git config on this host (`tobias.mueller@sitasys.com`),
  per the no-AI-attribution / correct-author requirement.
- Single Go module (`github.com/muelli/synctang`) holding `mrcore`,
  `transport`, `cmd/unlocker`, `cmd/keyholder`, rather than separate modules
  per component: it is one repository with one release cadence, and
  `gomobile bind` works fine against a package inside a larger module.
- `LICENSE` uses the plain AGPL-3.0 text (that is the only canonical text the
  FSF publishes); "or later" is expressed via the SPDX header and the
  `debian/copyright` grant, per standard practice, not by editing the
  licence body.
- AEAD is AES-256-GCM, not ChaCha20-Poly1305 (plan section 2 allowed either):
  it is in the standard library (`crypto/aes` + `crypto/cipher`), so it adds
  no third-party dependency, and both are equally fine choices for this
  threat model.
- Test vector JSON keys use `s_scalar`/`c_scalar`/`e_scalar` rather than
  `s`/`c`/`e` alongside `S`/`C`/`E`: Go's `encoding/json` folds JSON object
  keys that differ only in case onto the same struct field and lets the
  later key in document order silently win. The first version of
  `testdata/mr1.json` had `e`/`E` in the same object and a 32-byte scalar
  got silently overwritten by a 65-byte point during decode. Worth knowing
  for the Kotlin side too if it ever parses this file directly.
- Also given to that task: `keyholder`'s long-term MR-1 scalar file and
  its transport identity file should stay separate PEM/hex files rather
  than one combined file: they are different key types (a raw P-256
  scalar versus a TLS keypair) serving different protocols (MR-1 versus
  the transport handshake), and keeping them separate means either can be
  rotated or backed up independently.
- The MR-1 wire messages (`RecoverRequest`, `RecoverResponse`, `Hello`,
  `ConfirmCodeChallenge`/`Response`) live in `mrcore`, not duplicated in
  `cmd/unlocker` and `cmd/keyholder`: both binaries must agree on the
  exact shape or recovery simply fails, so there is exactly one
  definition. `WriteMessage`/`ReadMessage` take an `io.Writer`/`io.Reader`
  the caller already owns and perform no I/O of their own (no file or
  socket opened inside `mrcore`), so this does not reopen WP1's "no I/O"
  boundary in the sense that mattered there (no hidden dependency on the
  filesystem or network from a package that is supposed to be pure math
  plus encoding).
- Design given to the delegated `agent`/`unlock` task (not yet confirmed
  built as of this line): `unlocker`'s and `keyholder`'s persistent
  transport identities (separate from the MR-1 keypairs) should be plain
  self-signed TLS certs stored as PEM, loaded once and reused: the
  Syncthing Device ID is a hash of the certificate's DER bytes, so the
  same cert bytes must survive every restart or a machine's or key
  holder's transport identity would change every time it runs, breaking
  every stored `Recipient.Transport` / `AuthorizedPeers` match against it.

### Open [U] items

- `[V]` `filippo.io/nistec` v0.0.4's exported API (`Add`, `ScalarMult`,
  `ScalarBaseMult`, `SetBytes`, `Bytes`, `BytesX`) is sufficient for MR-1;
  no fork or vendoring needed, no unexported internals used. Verified by
  reading `p256.go` in the module cache and building `mrcore.Group` on top
  of it, all tests passing.
- `[U]` Whether `go-tpm`'s ECDH support is reachable in the time budgeted
  for the `keyholder` TPM2 backend (WP4); may remain a stub with tests
  skipped, as the plan allows.
- `[U]` Whether the existing `syncthing-socket` F-Droid pipeline (if any) is
  reusable for the Android app, per plan section 4; `android/app/build.gradle.kts`
  has not been inspected yet because no such Android app in that repository
  has been located yet; to check when starting WP5.

## 2026-09-19, session 2

### Done

- WP2 fix: `SyncthingRelay.dialRelay`'s retry loop only covered the
  discovery lookup and relay session join (`dialRelayOnce`); `Dial()`
  then called `clientHandshake()` as a separate step outside that loop.
  A real-VM retest after the previous (lookup/join-only) fix still failed
  with `transport: client handshake: EOF`, i.e. exactly the step the
  retry did not cover. Fixed by introducing `dialRelayAttempt`, which
  wraps the lookup, join and handshake together so any of the three
  failing triggers a fresh attempt; `dialRelay` now returns `(Conn,
  error)` directly rather than a raw `net.Conn`. Commit `6861f0d`.
  `gofmt`, `go vet`, and the full suite (`go test ./...`) all green
  after the change.

### In progress

- A3 retest against `synctang-test-2604` with the rebuilt `keyholder`:
  progress, but not yet passing. The relay dial and TLS handshake now
  succeed (confirming the WP2 fix above works, and that the machine's
  Device ID checks out over the relay). The next step now fails
  instead: `keyholder: reading hello: mrcore: reading message length:
  EOF`, meaning the machine's `unlocker` agent closes the connection
  right after the handshake, before ever writing its `Hello` message.
  Two candidate causes in `cmd/unlocker/agent.go`'s `runAgentOnce`,
  indistinguishable from the key holder's side (both just look like
  EOF):
  1. `transport.Listen()`'s `serverHandshake` rejected the key holder's
     certificate as unauthorized (logged server-side only, at Warn
     level).
  2. The handshake-level check passed, but `runAgentOnce`'s own
     redundant `matched == nil` check (comparing `conn.PeerID()` against
     each enrolled `Recipient.TransportDeviceID()`) failed instead,
     returning `unlocker: connected peer %s does not match any enrolled
     recipient` without ever writing `Hello`.
  Asked the separate hypervisor-access session (the same one that
  converted `synctang-test-2604`'s root to LUKS2) to check the VM's
  journal for the actual rejection reason and to compare the enrolled
  recipient's `device_id` against the current key holder identity's
  transport ID (`IL2O3W3-UX6ZIVO-NJHVUDL-LSXQ34Q-ALHXKES-3JGHIS3-W7ZPIBE-JXB57A7`,
  from `~/wp7-keyholder-config/`, regenerated after the `/tmp` wipe
  described in session 1). `[U]` pending that report; likely either a
  stale enrolment (re-enrol with the current key) or a real bug in one
  of the two checks above, not yet known which.
