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
`test-idle-shutdown.sh` (thirteen cases, including that mutating the script
makes the right ones fail). What is **not** tested is the real thing on a
real instance: that `sshd` names its processes as the script expects
(`user@notty`, `user@pts/N`, and a bare `sshd: user` for a tunnel), which is
`[I]` from how OpenSSH normally behaves and has not been seen here.

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

**Not** checked, because it needs an AWS account and a live instance:

- that EC2 accepts the launch (`up --dry-run` is how to find out cheaply);
- that the instance type is offered in your region with nested
  virtualization (the script checks and falls back along `EMU_INSTANCE_TYPES`);
- the first-boot setup on a real Ubuntu 26.04 instance, and that the
  emulator then boots with KVM inside the container. The container could not
  reach `/dev/kvm` on the machine it was tested on, because that machine's
  podman uses `runc` and the node is `root:kvm 0660`; the VM works around
  that with a udev rule, which is `[I]` and untested.
