# Status

Current state only, kept up to date in place; not a session log. History
lives in `git log`. Conventions and hard-won gotchas live in `AGENTS.md`,
not here. See `TESTREPORT.md` for the acceptance table (A1-A11).

## Work packages

- **WP0** (curve/feasibility spike): done at emulator level. Real
  StrongBox + biometric retest still needs a human with a suitable
  Android phone (see `docs/wp0-keystore-ecdh.md`, "For the human"):
  the one genuine external blocker left in this project.
- **WP1** (`mrcore` math): done.
- **WP2** (transport): done. `SyncthingRelay` (public relay + global
  discovery), `LocalDiscovery` (LAN multicast, no Internet needed),
  and `Multi` (races the two). Battle-tested against a real
  deployment; see `AGENTS.md` for what that surfaced.
- **WP3** (`unlocker`): `enrol` (add or `--remove` a recipient) and
  `agent` done. `status` and `pair` not started.
- **WP4** (`keyholder`): `init`/`unlock`/`export-pubkey` (file backend)
  done. `pair` and the TPM2 backend not started.
- **WP5** (Android app): built (`gomobile bind` + Kotlin UI), passes
  its own instrumented tests on an emulator, CI builds and tests it.
  Real-device retest is WP0's blocker above, shared.
- **WP6** (dracut module `90unlocker`): done. `enrol` auto-rebuilding
  the initrd is not wired in; currently a manual `dracut --force` step.
- **WP7** (VM acceptance): see `TESTREPORT.md`. A1, A2, A3, A5, A6,
  A7, A9, A10 and A11 verified on the real VM. A8 is verified by test
  but not yet end to end on the VM. A4 is the only one blocked, on
  real hardware (WP0).

## CI

Green: `go-test`, `go-test-privileged`, `lint`, `em-dash-check`,
`android` (the last one for the first time as of `59614d8`; see
`AGENTS.md` for the four issues chasing that down turned up), and
`build-deb`.

## Packaging

`build-deb` produces three Debian packages for amd64 and arm64:
`synctang-unlocker` (`/usr/sbin/unlocker`), `synctang-keyholder`
(`/usr/bin/keyholder`), and `synctang-dracut` (the 90unlocker module,
arch: all). It verifies the cross-built package really holds an aarch64
binary, attests build provenance with
`actions/attest-build-provenance` (pushes to `main` only, since a fork
pull request cannot get an id-token), and uploads the packages.

`just deb` builds them locally; `just deb-repro` builds twice under
different umask, `TZ` and `LC_ALL` and checks the results are
byte-identical, which CI also runs. Reproducibility rests on
`SOURCE_DATE_EPOCH` coming from the commit date rather than wall clock,
and on `go build -buildvcs=false -trimpath` with `CGO_ENABLED=0`. Scope
is deliberately limited to "same commit, matching toolchain and
packaging tool versions"; cross-machine and cross-architecture
reproducibility are untested and not claimed.

## Open items

- `[U]` Whether `go-tpm`'s ECDH support is reachable for the
  `keyholder` TPM2 backend; may remain a stub with tests skipped.
- `[U]` Three fixes for `syncthing-socket` (bare `go.mod` module path,
  `ensureResolver` accepting a loopback-only stub, `RunClient` not
  retrying a stale discovery record) were reported to the user as a
  written summary; not yet turned into patches or a PR, pending their
  say.
- `[U]` A `mrcore`/transport code-review pass found several real
  simplifications beyond the bugs already fixed (a `Token`
  Marshal/Unmarshal refactor, `Group.Negate` via `ScalarMult(N-1)`,
  `slices.Contains` instead of a hand-rolled equivalent, caching the
  discovery HTTP client); not applied, undecided whether before or
  after the rest of WP7 closes out.
- `[U]` `LocalDiscovery` is IPv4 multicast only. A LAN that is IPv6
  only would fall back to `SyncthingRelay`, which defeats the point
  on a LAN with no Internet. Not hit in testing (every network this
  has run on had IPv4), so not built.
