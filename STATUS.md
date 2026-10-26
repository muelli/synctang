<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Status

Current state only, kept up to date in place; not a session log. History
lives in `git log`. Conventions and hard-won gotchas live in `AGENTS.md`,
not here. See `TESTREPORT.md` for the acceptance table (A1-A11).

## Work packages

- **WP0** (curve/feasibility spike): **done, including on real
  hardware**. A StrongBox-held, biometric-gated key performed ECDH
  against a hand-built peer point and returned the correct value, as
  proved by the machine opening its volume with it (A4). The emulator
  run could only show that a software Keystore accepted such a point.
- **WP1** (`mrcore` math): done.
- **WP2** (transport): done. `SyncthingRelay` (public relay + global
  discovery), `LocalDiscovery` (LAN multicast, no Internet needed),
  and `Multi` (races the two). Battle-tested against a real
  deployment; see `AGENTS.md` for what that surfaced.
- **WP3** (`unlocker`): `enrol` (add, `--remove`, or `--replace` a
  recipient), `agent`, `status` and `pair` all done. `pair` shows a QR
  code carrying this machine's Device ID and a one-time code, then
  enrols the key holder that answers it, so a phone no longer needs its
  public key copied across by hand.
- **WP4** (`keyholder`): `init`/`unlock`/`export-pubkey` (file backend)
  done. The TPM2 backend is not started, and neither is `keyholder
  pair`: the laptop can still only be enrolled the manual way
  (`export-pubkey` into `unlocker enrol`), since the QR-code path was
  built for the phone, where copying a public key across is worst. The
  machine side is transport-agnostic, so a laptop `pair` is a client
  for the protocol that already exists rather than new protocol work.
- **WP5** (Android app): built (`gomobile bind` + Kotlin UI), passes
  its own instrumented tests on an emulator, CI builds and tests it,
  and is keyboard-navigable for Android's desktop mode. Run on real
  hardware for the first time on 2026-09-20, which immediately found
  three bugs no test had: it misread the order of the machine's
  messages (it guessed their types by field presence rather than
  reading them positionally), it gave up after a single
  attempt where the laptop client retries, and it reported success on
  sending its answer rather than on the volume actually opening. All
  three are fixed: the last by `mrcore.RecoverResult`, the machine's
  own verdict, which the app now waits for before claiming anything.
  Since 2026-09-21 the app races `LocalDiscovery` against the relay
  like the laptop client does, holding a Wi-Fi `MulticastLock` for the
  duration of a dial, so a phone can unlock a machine on the same
  network with no Internet. Covered by a Go test that connects a
  `Session` to a machine reachable only by multicast, and by Kotlin
  tests for the lock's lifetime; not yet exercised on real hardware.
  A pairing with a one-time code saves the machine only once the
  machine has enrolled the phone, so a failed one offers "Scan again"
  and leaves any existing pairing alone; Ready can pair again without
  forgetting anything first, and a pairing that replaces a different
  machine is offered back by an Undo snackbar. Connecting and pairing can be cancelled
  (`Session.Cancel`, `Pairer.Cancel` interrupt the Go call), and back
  on a screen where the machine is waiting declines as Decline does.
  Once connected, each wait for the machine is bounded (60 s), so a
  machine that goes silent mid-exchange cannot hold the phone for ever.
  Failures are explained in the app's own words rather than as Go error
  text: the Go bridge names each one (`Session.LastFailure`,
  `Pairer.LastFailure`) and the app maps the kind to a message, showing
  the machine's own reason where it gave one and the raw text behind a
  Details toggle; every failure is also logged. The manual enrolment
  command, the phone's full Device ID and public key, how its key is
  held, and the app version live on an About this phone screen reached
  from the app bar, offered only where nothing is under way. Every
  screen shares one layout (title, explanation, one main action; a
  glyph and headline on Done and Failed), and long identifiers show in
  a short form with the full value one tap away. Each screen state has
  a Compose `@Preview` in `ui/Previews.kt`; none of the layouts has been
  looked at on a screen or in a rendered preview yet `[U]`. A key
  invalidated by a newly enrolled fingerprint now gets a fresh key and
  transport identity and says which old Device ID to revoke `[V]` in
  JVM tests; the Keystore exceptions it relies on have not been
  provoked on a real device `[U]`.
  The flow's decisions live in `UnlockFlow`, tested on the JVM through
  fakes `[V]`; none of this has been run on a device or emulator yet
  `[U]`.
  `-Psynctang.noBiometric=true` builds a debug-only variant whose key
  is not gated behind a biometric prompt, so the unlock flow can be
  driven end to end without a human; release builds cannot have it and
  the ungated key uses its own Keystore alias.
- **WP6** (dracut module `90unlocker`): done. `enrol` auto-rebuilding
  the initrd is not wired in; currently a manual `dracut --force` step.
- **WP7** (VM acceptance): **all eleven acceptance items pass**; see
  `TESTREPORT.md`. A4 was the last, verified 2026-09-21 on a real
  StrongBox phone against a machine that had been waiting at its LUKS
  prompt for over eleven hours.

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

## Adversarial testing

Both ends assume the other may be lying, and both directions are
tested that way. Against a key holder: off-curve points (the classical
invalid-curve attack, refused by the group), non-canonical
coordinates, malformed encodings and scalars, and the point at
infinity (accepted by the group, since `s.O = O` leaks nothing, but
refused by `ValidateChallengePoint` one layer up). Against a machine:
answers computed with the wrong scalar, off-curve answers, and six
shapes of malformed response, none of which reach the ask-password
socket. Against the transport: hostile multicast announcements,
impossible ports, and an impostor that answers with a valid
certificate of its own.

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

- `[U]` Findings of the external security review of `a57ae27` (2026-10-23)
  that are still open. Fixed since: silent peers holding a listener or the
  pairing window, the phone validating the challenge point only after the
  Keystore, empty `PeerID` dials and empty recipient lists failing open,
  unfiltered peer strings, recipient transport ids not canonicalised,
  non-uniform scalars, no length check on the recovered secret, and the
  documents that overstated the confirm code, and the confirm code
  itself, removed because the machine sent it to the key holder it was
  meant to test. Open:
  - A disk image plus one approval recovers the volume secret. The fix is
    sealing the machine identity in a TPM (not built).
  - The Android key is usable for a 15 second window after any strong
    biometric, not bound to the request.
  - `[U]` A threshold (two key holders must both answer) is the second
    factor that would remove "one approval is enough". Parked for a
    later round; see `docs/threat-model.md`.
  - `PairProof` does not cover the public key being enrolled. Not
    changed because it alters the pairing exchange and a phone and a
    machine on different versions would stop pairing; it needs a
    versioned bump, and there is no attack today because the pinned
    channel already stops anyone but the proof's author altering it.
  - Whether the initrd that carries `machine.pem` is `0600` on a target
    distribution; scanning any QR code replaces the paired machine before
    pairing succeeds (Android); device auto-discovery takes the first
    block device with an `mr-1` token rather than the one being asked
    about.
- `[U]` Whether `go-tpm`'s ECDH support is reachable for the
  `keyholder` TPM2 backend; may remain a stub with tests skipped.
- `[U]` Two fixes for `syncthing-socket` (`ensureResolver` accepting a
  loopback-only stub, `RunClient` not retrying a stale discovery
  record) were reported to the user as a written summary; whether they
  have become patches since was not checked. The third, the bare
  `go.mod` module path, is done: release v164 declares the real path and
  this repository imports its `identity` and `discovery` packages `[V]`.
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
