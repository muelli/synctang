<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Status

Current state only, kept up to date in place; not a session log. History
lives in `git log`. Conventions and hard-won gotchas live in `AGENTS.md`,
not here. The acceptance table (A1-A11) and the dated test runs were in
`TESTREPORT.md`, which is kept out of the repository now (decided 2026-11-02);
its last version is `git show 6a5c646:TESTREPORT.md`. Mentions of it below and in
`docs/` refer to that document. `[V]` marks what was run, `[I]` what was inferred;
what was not tried or is not known is said in words.

## What works

A machine at its LUKS prompt can be unlocked from a laptop or from a phone, by a
person, without the passphrase leaving the key holder's side of the exchange.
`[V]` on 2026-11-03 and 2026-11-04:

- A Fedora 44 VM (SELinux enforcing, dracut 108) and an Ubuntu 26.04 VM built from
  the `.deb` packages, each unlocked from the laptop key holder and from a Pixel 3a XL,
  with nobody typing a passphrase. The Ubuntu VM was unlocked with the F-Droid build of
  the app (versions 290 and 299, the latter with the one-tap flow).
- A second phone, tried by the owner on 2026-11-10 (pairing and unlock), worked.
- Through a relay and a discovery server of the owner's own on a private bridge with no
  route out, with nothing sent to the public servers.
- `scripts/e2e-vm.sh` does the VM half of that automatically, with a headless phone in
  place of the real one: it passes locally and in CI (`docs/e2e-test.md`).

There is no compatibility promise between releases. A new release may need a phone paired
again.

## Work packages

- **WP0** (curve/feasibility spike): done, including on real hardware. A StrongBox-held,
  biometric-gated key performed ECDH against a hand-built peer point and returned the
  correct value, as proved by the machine opening its volume with it (A4). That was one
  phone: on a Pixel 3a XL (Android 12, Keymaster 4.x) a key-agreement key is created in
  software, not in StrongBox or the TEE (`docs/threat-model.md`), so hardware key
  agreement is not something every phone can deliver. Since 2026-11-01 the app does not
  use a Keystore agreement key at all: it keeps its own scalar wrapped by a hardware key
  that needs a fingerprint per use (`docs/threat-model.md`).
- **WP1** (`mrcore` math): done.
- **WP2** (transport): done. `SyncthingRelay` (public relay pool or a relay and
  discovery server of the owner's own, `docs/private-servers.md`), `LocalDiscovery`
  (LAN multicast, no Internet needed), and `Multi` (races the two). The machine
  re-announces every minute and rejoins a relay that drops it; a key holder's lookups
  back off and obey a 429's Retry-After. The relay pool's relays are measured at once,
  which takes a join from about 20 s to about 2 s (`docs/transport.md`).
- **WP3** (`synctang-unlocker`): `enrol` (add a recipient), `revoke` and `rotate` (remove a
  recipient, or give it a new key), `agent`, `status`, `pair`, `devices`, `rehearse` and `open`. One enrolled recipient per transport ID. The agent
  logs to the journal as `synctang-unlocker`, at the level `rd.synctang.log=` sets.
  Settings for private servers come from `/etc/synctang/network.conf` and `rd.synctang.*`
  kernel arguments. Manual pages for every command, flag, file and kernel argument are
  checked against the code by tests.
- **WP4** (`synctang-keyholder`): `pair`, `init`, `export-pubkey`, `unlock`, `machines` and `status`. The key is
  kept by a store chosen by `init --store`: the desktop keyring through the Secret Service (`go-keyring`),
  `systemd-creds` in the user's own scope (and a `:tpm2` variant), or a plain file, which is the fallback
  when there is no keyring, with a warning. Each store says what it protects and what it does not
  (`docs/threat-model.md`); none stops software running as the user. Tested with the library's mock and a fake
  tool, and against a real `gnome-keyring-daemon` on a private session bus and the real `systemd-creds`
  (user scope, systemd 259) where they are installed. The TPM variant has not been run on a TPM, as the
  test VM has none. The keyring is GNOME Keyring; no other Secret Service is supported or tried. `synctang-keyholder pair` enrols a laptop from the link
  `synctang-unlocker pair` shows, the same protocol as a phone, and both sides show a six-digit code to
  compare (`mrcore.PairSAS`); the phone app shows it on its pairing screen while the operator decides. `unlock` takes a machine's name, kept in `~/.config/synctang-keyholder/machines`.
  `synctang-unlocker enrol` remains, with `--recipient-transport-id` required.
- **WP5** (Android app): `gomobile bind` plus a Kotlin and Compose UI.
  Unlock is one tap: it connects, and the fingerprint prompt comes up when the
  machine's request arrives, naming the machine; cancelling the prompt returns to an
  Approve screen with the window still running. A readiness chip (green, orange, red, with
  words) shows whether the paired machine has announced itself, from one lookup a minute
  with jitter, honouring a 429; a long press or "Check again" looks now. Busy screens say
  what the app is doing (looking up, connecting, waiting for the machine). The approve
  window is 75 s from the connection and runs through the fingerprint prompt
  (`docs/relay-idle.md`). The key is a P-256 scalar wrapped by an RSA Keystore key that
  needs a fingerprint for each use; a phone whose key is not whole says so and offers a
  new one. The app is keyboard-navigable, has headings, live regions and a shortcut
  table, and debug builds log at trace level (`docs/debugging.md`).
  Tests: JVM tests of the flow through fakes, and instrumented Compose tests on an
  emulator in CI; the chip's tests were also run on the Pixel `[V]`. The listing's five
  screenshots are drawn by `ScreenshotsTest` (`scripts/screenshots.sh`). Not tried: a
  screen reader (TalkBack) and the Keystore exceptions for a key invalidated by a new
  fingerprint on a real device (JVM tests only).
  `-Psynctang.noBiometric=true` builds a debug-only variant whose key is not gated
  behind a biometric prompt; release builds cannot have it and the ungated key uses its
  own Keystore alias.
- **WP6** (dracut module `90synctang`): done. The module carries the machine identity,
  `network.conf` and the CA bundle. Installing `synctang-dracut` rebuilds every installed
  kernel's initrd, which is before the identity exists; `pair`, `enrol` and `status`
  compare the initrd of the running kernel with the identity and `network.conf` by hash
  and say so when it is wrong, and `--rebuild-initrd` on `pair` and `enrol` runs
  `dracut --force` when it is. Only the running kernel's initrd is checked.
  `rehearse` goes further without a reboot: it checks the initrd's modules, that the agent in it
  is the installed one and that the kernel command line leaves the agent on, then runs the
  agent's own exchange with a key holder and checks the recovered secret against a keyslot,
  mapping nothing. It cannot test the network in early boot or the initrd starting.
- **WP7** (VM acceptance): all eleven acceptance items pass (`TESTREPORT.md`).

## Test machines

- A shared VM on someone else's hypervisor (`synctang-test-2604`), where the boot-time
  acceptance runs against a real deployment happened. This side does not hold its
  passphrase, so anything that might leave it unbootable cannot be run there.
- A disposable local VM, built by `scripts/make-test-vm.sh` and run by
  `scripts/run-test-vm.sh` under QEMU with KVM: a real LUKS2 root, dracut initrd, serial
  console on a TCP socket, and a passphrase this side chose. `scripts/test-vm-net.sh`
  puts it on a private bridge that shares a real layer 2 segment with the host and has no
  route off it. Rebuild costs about five minutes, so it is genuinely throwaway: A8
  destroys a keyslot on it, which is exactly why it exists. `scripts/e2e-vm.sh` builds one,
  installs the packages and unlocks it twice (`docs/e2e-test.md`).
- A Fedora 44 VM (kept outside the repository) for SELinux-enforcing and dracut 108 runs.

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

Workflows, each run on every pull request unless noted:

- `ci`: `go-test` (with the relay and discovery servers built from source, so the
  integration tests run), `go-test-privileged` (real LUKS images), `lint` (shellcheck and
  the shell tests of the dracut module and man pages), `em-dash-check`, `reuse`,
  `android` (JVM tests and a debug build), `build-deb` (below) and `build-apk`. On a
  `v<versionCode>` tag it also runs `e2e` and then `release-debs`.
- `emulator`: the instrumented tests on an Android emulator with KVM, which include
  `ScreenshotsTest`. Pushes to `main` run it only when `android/`, `mobile/` or `mrcore/`
  changed.
- `e2e`: `scripts/e2e-vm.sh` on a KVM runner. Pushes to `main` run it only when what it
  tests changed.
- `publish-fdroid`: on a push to `main` it publishes a release-candidate build to the
  self-hosted F-Droid repository; on a tag, a release.

## Packaging and releases

`build-deb` produces three Debian packages for amd64 and arm64:
`synctang-unlocker` (`/usr/sbin/synctang-unlocker`), `synctang-keyholder`
(`/usr/bin/synctang-keyholder`), and `synctang-dracut` (the 90synctang module,
arch: all). It verifies the cross-built package really holds an aarch64 binary,
checks the build is reproducible, makes SBOMs, attests build provenance (pushes to
`main` and tags only, since a fork pull request cannot get an id-token), and uploads
the packages.

`just deb` builds them locally; `just deb-repro` builds twice under different umask,
`TZ` and `LC_ALL` and checks the results are byte-identical, which CI also runs.
Reproducibility rests on `SOURCE_DATE_EPOCH` coming from the commit date rather than
wall clock, and on `go build -buildvcs=false -trimpath` with `CGO_ENABLED=0`. Scope is
deliberately limited to "same commit, matching toolchain and packaging tool versions";
cross-machine and cross-architecture reproducibility are untested and not claimed.

On a tag, `release-debs` attaches the packages, the SBOMs and a `SHA256SUMS` file to a
GitHub release, each with a cosign bundle, once `e2e` has passed on those same packages
(`docs/releasing.md`). That job, the cosign signing and the tag run of the F-Droid
publish have never run: every release so far has been a release candidate from `main`.

## Open items

- Debian and Ubuntu machines that boot with initramfs-tools. `synctang-dracut`
  depends on dracut and regenerates every installed kernel's initrd with it, which would
  replace initramfs-tools images. Every machine tested here used dracut from the start.
- A real machine (a physical NIC, the owner's own network).
- Reliability: one of the five relay integration tests failed once in thirteen runs with
  "already connected" (a join race, not fixed); one dial gave up after 90 s with every
  relay in the record "not found" and was not reproduced; and the machine took about 17 to
  19 s to check the recovered secret on a 2-vCPU VM with a real key derivation, with
  nothing shown to the person meanwhile.
- A third security review is not written. Scope: the dracut module and the agent's shell,
  the laptop key holder, the QR scanner, the packaging, the journal writer, the
  discovery trust code and the readiness polling. The relay and discovery servers are out
  of scope (they are Syncthing's).
- Open findings of the external security review of `a57ae27` (2026-10-23):
  - A disk image plus one approval recovers the volume secret. The fix is sealing the machine
    identity in a TPM (not built).
  - A threshold (two key holders must both answer) is the second factor that would remove "one
    approval is enough". Parked for a later round; see `docs/threat-model.md`.
  - Not observed on a running system: that a password request's `Id=` has the form
    `cryptsetup:<device path>` (`[I]`, from systemd's source); the device choice is tested with fake ids.
- Two fixes for `syncthing-socket` (`ensureResolver` accepting a loopback-only stub, `RunClient` not
  retrying a stale discovery record) were reported as a written summary; whether they became patches
  was not checked.
- A `Token` Marshal/Unmarshal refactor in `mrcore` is left.
- `LocalDiscovery` is IPv4 multicast only. A LAN that is IPv6
  only would fall back to `SyncthingRelay`, which defeats the point
  on a LAN with no Internet. Not hit in testing (every network this
  has run on had IPv4), so not built.
