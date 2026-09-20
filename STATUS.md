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
  its own instrumented tests on an emulator, CI builds and tests it,
  and is keyboard-navigable for Android's desktop mode. Run on real
  hardware for the first time on 2026-09-20, which immediately found
  three bugs no test had: it could not unlock a machine that was not
  in confirm-code mode (it guessed message types by field presence
  rather than reading them positionally), it gave up after a single
  attempt where the laptop client retries, and it reports success on
  sending its answer rather than on the volume actually opening. The
  first two are fixed; the third is not.
  `-Psynctang.noBiometric=true` builds a debug-only variant whose key
  is not gated behind a biometric prompt, so the unlock flow can be
  driven end to end without a human; release builds cannot have it and
  the ungated key uses its own Keystore alias.
- **WP6** (dracut module `90unlocker`): done. `enrol` auto-rebuilding
  the initrd is not wired in; currently a manual `dracut --force` step.
- **WP7** (VM acceptance): see `TESTREPORT.md`. A1, A2, A3, A5, A6,
  A7, A8, A9, A10 and A11 all verified. A4 is the only one left, and
  the only one blocked: it needs real StrongBox hardware (WP0).

## Test VMs

Two, for different jobs.

- A shared VM on someone else's hypervisor (`synctang-test-2604`),
  which is where the boot-time acceptance runs against a real
  deployment happened. This side does not hold its passphrase, so
  anything that might leave it unbootable cannot be run there.
- A disposable local one, built by `scripts/make-test-vm.sh` and run
  by `scripts/run-test-vm.sh` under QEMU with nested KVM: a real
  LUKS2 root, dracut initrd, serial console on a TCP socket, and a
  passphrase this side chose. `scripts/provision-test-vm.sh` installs
  the unlocker, the dracut module and enrolled recipients into the
  image over a loop device, without booting it.
  `scripts/test-vm-net.sh` puts it on a private bridge so it shares a
  real layer 2 segment with the host, which local discovery needs and
  QEMU's user-mode networking cannot provide; that bridge has no
  route off it, so it is also A11's condition by default. Rebuild
  costs about four minutes, so it is genuinely throwaway: A8 destroys
  a keyslot on it, which is exactly why it exists.

## Fuzzing

`mrcore/fuzz_test.go` covers the code that parses bytes chosen by
somebody else: the wire messages, the LUKS2 token, and the x-only
recovery path, which lifts a peer-supplied coordinate to a curve
point. The property asserted is only "never panic", which is the one
that matters for a machine at a LUKS prompt with nobody there to
restart it. About 2.5 million executions found nothing. CI runs each
target for 20 seconds, enough to catch a target that has stopped
building rather than to mount a campaign.

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
- `[U]` One item left from the `mrcore`/transport review: a `Token`
  Marshal/Unmarshal refactor. The other three are done: `Group.Negate`
  now multiplies by the group order minus one instead of doing modular
  arithmetic on the y-coordinate with `big.Int`, which removes the
  last hand-rolled field arithmetic in the package and hands it to
  nistec's constant-time code; `slices.Contains`/`slices.Equal`
  replace the hand-rolled equivalents; the announce HTTP client is
  built once rather than per call.
- `[U]` `LocalDiscovery` is IPv4 multicast only. A LAN that is IPv6
  only would fall back to `SyncthingRelay`, which defeats the point
  on a LAN with no Internet. Not hit in testing (every network this
  has run on had IPv4), so not built.
