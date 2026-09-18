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

### In progress

- WP0 (Android Keystore ECDH spike): delegated to a sub-agent to set up the
  Android SDK and emulator, write the Kotlin scratch app, and run it without
  biometrics on the emulator, per plan section 7 / WP0.
- WP1 (`mrcore` math): starting in parallel. Decision: the plan's "WP0 gates
  the curve; do not start WP1 until WP0 has a result" is read as gating on
  the emulator-level feasibility result (ECDH against an arbitrary
  constructed point returns a usable x-coordinate), not on real-device
  StrongBox/biometric confirmation, because the curve itself (P-256) is
  already locked in the plan's "Decisions locked" section independently of
  WP0's outcome. `mrcore`'s point arithmetic does not change if WP0's
  fallback (software-encrypted key) is later needed; only the Android
  key-holder's key storage does. Recorded here per "pick one, note it, move
  on."

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

### Open [U] items

- `[U]` Whether `filippo.io/nistec` exposes the point operations needed
  (add, subtract, scalar multiply, encode/decode) at the API stability level
  required, or whether it needs to be forked/vendored. To check in WP1.
  See `[V]` claim in the plan section 2 that it exists; API shape not yet
  verified against actual code.
- `[U]` Whether `go-tpm`'s ECDH support is reachable in the time budgeted
  for the `keyholder` TPM2 backend (WP4); may remain a stub with tests
  skipped, as the plan allows.
- `[U]` Whether the existing `syncthing-socket` F-Droid pipeline (if any) is
  reusable for the Android app, per plan section 4; `android/app/build.gradle.kts`
  has not been inspected yet because no such Android app in that repository
  has been located yet; to check when starting WP5.
