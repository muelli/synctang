<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# An Android emulator VM on AWS

The Android emulator needs KVM and CPUs nothing else is using. The machines
this project is usually worked on have neither to spare, so this makes a
disposable EC2 instance for it. It **stops itself after an hour without
use**, so a forgotten instance does not keep billing.

## Use

You need the AWS CLI, an ssh key (`~/.ssh/id_ed25519`), and credentials for
the profile `code-poweruser` (override with `AWS_PROFILE`; region
`eu-central-1`, override with `AWS_REGION`).

    aws sso login --profile code-poweruser
    scripts/emulator-vm/emu-ctl.sh up --dry-run   # checks you may launch it; creates nothing
    scripts/emulator-vm/emu-ctl.sh up             # creates it, or starts it if stopped
    scripts/emulator-vm/emu-ctl.sh wait-ready     # first time only, about ten minutes
    scripts/emulator-vm/emu-ctl.sh ssh

On the VM, the build environment is a container image, `synctang-emulator`
(JDK 21, Go, the Android SDK, an API 34 x86_64 emulator image):

    cd /opt/synctang                              # a clone of the repository
    podman run --rm -it --device /dev/kvm -v "$PWD":/work -w /work synctang-emulator bash
    # inside the container:
    go install golang.org/x/mobile/cmd/gomobile@latest golang.org/x/mobile/cmd/gobind@latest
    scripts/ci-boot-emulator.sh ci
    cd android && ./gradlew connectedDebugAndroidTest -Psynctang.goMobileTarget=android/amd64

The other subcommands are `status`, `logs` (the first-boot setup log),
`keepalive`, `phone-tunnel`, `stop` and `destroy --yes`; run `emu-ctl.sh`
with no arguments for the list.

## When it stops itself

`idle-shutdown.sh` runs every five minutes and powers the instance off when
it has seen no use for `EMU_IDLE_MINUTES` (default 60). The instance is
configured so a shutdown from inside **stops** it: the disk and the installed
image survive, and `up` starts it again in about a minute. Something counts
as use if:

- an ssh session running a command exists (it counts for as long as the
  command runs, so a long build or an agent driving the VM keeps it on);
- an interactive ssh session had terminal input or output in the last
  `EMU_IDLE_MINUTES`;
- `emu-ctl.sh keepalive` was run in the last `EMU_IDLE_MINUTES`, for a job
  nobody is logged in for.

A bare port forward, such as the phone tunnel below, is **not** use, so
leaving one open does not keep the VM running. Whatever else is going on, it
also powers off once it has been running for `EMU_MAX_RUNTIME_HOURS`
(default 10), so a session that is never closed cannot run for ever.

The decisions are tested with a fake clock and fake process list:
`test-idle-shutdown.sh` (twenty-three cases, and breaking the script makes the
right ones fail). It was also tested on a real instance (2026-10-28), which
found a bug the fake tests could not: OpenSSH 9.8 and later, including the
10.2 in Ubuntu 26.04, names a session process `sshd-session: user@notty` or
`sshd-session: user@pts/N`, not `sshd:`, so the first version saw no session
at all and would have powered the VM off in the middle of a build `[V]`. Both
names are recognised now, with the real process titles in the tests.

A second flaw was found by reasoning about what the first real instance's
journal showed, not by running anything: the script recorded the time of each
check that saw use as the time of last use, so a keepalive (or a terminal)
counted as use for an hour and then the instance waited a further hour idle,
about two hours in all instead of one. It now records the activity's own
timestamp. The test that catches it runs the script twice on the same state,
at 56 and at 61 minutes, and fails against the old script.

Verified on a real instance `[V]`:

- a running ssh command is reported as use (`sshd-session: ubuntu@notty`);
- a bare tunnel shows up as `sshd-session: ubuntu` (no `@`) and does **not**
  count, and the instance powered itself off with a tunnel still attached;
- with nothing connected the instance went from `running` to `stopped`
  (not terminated) within seconds of the timer firing, and `up` started it
  again, with a new public address, the disk and the image intact.

Not verified: an interactive session with a terminal (`@pts/N`) was seen to be
named as expected but its idle-timeout was not waited out on the real
instance.

## What it costs

Compute is billed while the instance is `running` only. A stopped instance
still has its disk (`EMU_VOLUME_GB`, default 100 GB, encrypted gp3) and keeps
costing for that. `destroy --yes` removes everything. Prices were not looked
up `[U]`; nested virtualization itself has no extra charge, per AWS's
documentation.

## Using a phone

For the tests that need a real device (StrongBox, the biometric prompt),
plug the phone into the machine you work on and run, there:

    scripts/emulator-vm/emu-ctl.sh phone-tunnel --usb

On the VM, `adb -P 15037 devices` then shows it. If the phone offers adb
over the network instead, `emu-ctl.sh phone-tunnel <phone-host>[:5555]` and
`adb connect localhost:15555` on the VM. Both are `[I]`: not tried with a
real phone. The phone has to be unlocked while a biometric test runs, and
it cannot press your finger for you.

## Security

- ssh is open to **one address**, found with `checkip.amazonaws.com` (or
  `EMU_ALLOWED_CIDR`), and re-pointed on every `up`; an old address is closed.
- The instance requires IMDSv2 and has an encrypted disk. It has **no AWS
  role**: nothing on it can reach your account.
- It is a build machine. **Do not put signing seeds, a LUKS passphrase or
  any other secret on it.**
- The Android SDK is under Google's licence, which forbids redistributing
  it. **Never push the `synctang-emulator` image to a public registry.** See
  `THIRD_PARTY.md`.

## What has and has not been checked

Checked here, by running it:

- the Containerfile builds, the SDK download is verified against Google's
  checksum, and Java, Go, `sdkmanager` and `adb` run in the image;
- `emu-ctl.sh` against a stand-in `aws` (`test-emu-ctl.sh`), with every call
  also validated by the real AWS CLI's own argument checks, which catch
  misspelled options and malformed queries (confirmed by breaking the
  script on purpose);
- `idle-shutdown.sh` (above) and `shellcheck` on all of it.

Checked against AWS itself (`up --dry-run`, 2026-10-28, account of the
`code-poweruser` profile, `eu-central-1`):

- EC2 accepts the launch request, including the nested-virtualization option,
  the disk, the metadata options, the tags and the first-boot script;
- `c8i.2xlarge` is offered in `eu-central-1` with nested virtualization, in
  all three zones; the default VPC's default subnets are used.

That dry run also found a bug the stand-in missed: the CLI's text output
separates values with tabs, and the script built a zone filter from spaces.
The stand-in now answers with tabs as well, so the test would have caught it.

Checked on a real instance (2026-10-28) `[V]`:

- the first-boot setup completes on Ubuntu 26.04 in about fourteen minutes
  (most of it the SDK downloads) and builds the image (9 GB);
- the container opens `/dev/kvm` and the emulator's own check says KVM is
  installed and usable. The container could not do this on the machine it was
  first tested on, because that machine's podman uses `runc` and the node is
  `root:kvm 0660`; on the VM the runtime is `crun` and the udev rule makes the
  node world accessible.

- the emulator boots to Android inside the container in about 36 seconds
  (against about 190 on the shared machine it was first tried on), and the
  14 instrumented tests pass on it (Keystore ECDH against the Go side, the
  MR-1 test vectors, the keyboard-navigation tests);
- `scripts/screenshots.sh` runs end to end in the container in about two
  minutes, and produced the F-Droid listing screenshots. It also had two
  bugs that were never visible before, because it had never run to completion
  anywhere: it installed with every permission granted, so the pairing screen
  was filled by the emulator's camera test scene, and its pairing payload had
  a space where the `&` belongs, so it never reached the paired screen.

**Not** checked yet:

- the phone tunnel with a real phone;
- the emulator workflow on GitHub (`emulator.yml`): the first run there hung
  for an hour and its cause is still unknown; run it by hand from the
  Actions tab and send the log.
