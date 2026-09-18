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

### In progress

- WP0 (Android Keystore ECDH spike): delegated to a sub-agent to set up the
  Android SDK and emulator, write the Kotlin scratch app, and run it without
  biometrics on the emulator, per plan section 7 / WP0. Still running.
- WP2 (transport interface + syncthing-socket implementation): starting
  next.

### Blocked

- None currently. Real-device StrongBox/biometric testing for WP0 will need
  a human once the emulator-level spike is built and instructions are
  written (stop condition in the task brief).

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
