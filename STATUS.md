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

- WP4 (`keyholder`) in progress: `init`/`export-pubkey` (file backend)
  complete, need nothing beyond `mrcore`. `s` stored as hex in a 0600
  file under the user's config directory; `init` refuses to overwrite an
  existing key without `--force`. `pair`/`unlock` and the TPM2 backend
  behind a build tag not started; `unlock` is part of the task currently
  delegated (see WP3 below), TPM2 remains an open `[U]` item.

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

- WP3 (`unlocker`) in progress: `enrol` complete (T3.1, real cryptsetup,
  no root needed for header ops, see the loopback-LUKS note below).
  `agent`/`status`/`--confirm-code` (T3.2, T3.3) and WP4's `keyholder
  unlock`, plus shared transport-identity persistence, delegated as one
  task (they share a wire protocol and need testing together); running,
  no result yet as of this line. `rd.unlocker=0` is already fully handled
  at the dracut shell level (WP6), nothing further needed there.
  `pair` (live QR/network pairing, both sides) explicitly deferred: the
  laptop path is already complete without it (`keyholder export-pubkey` +
  `unlocker enrol --pubkey`); live pairing matters most for the phone,
  which needs the Android app (WP5) to exist first anyway.

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

### In progress

- Waiting on the `unlocker agent` / `keyholder unlock` delegated task
  (see above).

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
