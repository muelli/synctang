<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# The end-to-end test

`scripts/e2e-vm.sh` puts the packages in a real machine and unlocks it from a key
holder that is not on it. `[V]` marks what was run here, `[U]` what was not.

## What it does

1. Starts a relay (`strelaysrv`) and a discovery server (`stdiscosrv`), the Syncthing
   project's own, on a private bridge, `10.77.0.1`, that has no route out. Nothing
   in the test uses the public servers.
2. Builds a VM with a LUKS2 root and dracut (`scripts/make-test-vm.sh`), boots it, and
   types the passphrase once on its serial console, because nothing of synctang is
   on it yet.
3. Installs `synctang-unlocker` and `synctang-dracut` from the `.deb` files, and
   writes `/etc/synctang/network.conf` naming the private servers.
4. Enrols a laptop key holder (`synctang-keyholder`, from its `.deb`), pairs a
   headless phone from the link `synctang-unlocker pair` shows, and checks that
   both are enrolled. Rebuilds the initrd and checks that it carries the machine
   identity, `network.conf` and the agent.
5. Reboots to the passphrase prompt. A key holder that was never enrolled tries to
   unlock it and must get nowhere; then the laptop unlocks it.
6. Reboots again; the phone unlocks it.
7. Then the laptop's key in each store it can be kept in, each one a whole path: a key holder is *paired*
   with the machine the way a person does it (the machine shows a link and asks, the laptop is given that
   one string, makes its key there if it has none, and shows a code; the machine's operator says yes), and
   both must show the same six digits. Then it unlocks the machine after a reboot. The keyring (a private
   session bus and `gnome-keyring-daemon`, started by the script): the key is in the keyring and no key
   file was made, `status` says so and that it reads, the machine can be unlocked by the name it was paired
   under, with no discovery server named (the name brings it), a key holder with no reachable keyring is
   told that and not told there is no key, and the machine unlocks. And
   `systemd-creds` (user scope): a sealed blob and no key file, the key is not in it in the clear, and the machine
   unlocks. A leg whose tools are not there is skipped, and says so. (The first laptop, in step 3, is
   enrolled with `synctang-unlocker enrol`, the by-hand path.)
8. After each unlock it checks that the agent logged `recovery attempt succeeded` in
   the VM's journal, and that the passphrase was not typed on the console since the
   reboot.

The phone is `integration/e2ephone`: the app's own Go library (`mobile`: pairing,
the session, the answer) with its key in a file. It tests that code against a real
machine. It does not test the Keystore, the fingerprint or the Kotlin screens, which
are tried by hand on a phone.

`[V]` Run on this host (KVM, Ubuntu 26.04 guest, packages built from the commit before this line
was written), 39 checks passed and none failed (the package versions the two programs report among them), including the two laptop-key legs of step 7 (each paired, with the two codes compared) and the phone pairing (the code the Go library offers the app compared with the machine's), which
ran against a real `gnome-keyring-daemon` and the real `systemd-creds`; the TPM variant is not in it, as the
VM has no TPM. A key holder that was never
enrolled was refused by the machine at the first message (`reading hello: EOF`).

## Running it

    just deb                                              # the three .deb files, in dist/
    scripts/build-syncthing-servers.sh /tmp/stbin
    sudo env "PATH=$PATH" STRELAYSRV=/tmp/stbin/strelaysrv STDISCOSRV=/tmp/stbin/stdiscosrv \
        scripts/e2e-vm.sh

It needs root (a bridge, loop devices, a chroot), KVM, `qemu-system-x86`, `dnsmasq`,
`cryptsetup`, `sgdisk` and `ssh`. Everything is under `$WORK`
(`/var/tmp/synctang-e2e`); its `run/logs` directory has the console, the journal's
console, both servers, `apt` and each key holder's output, which is where to look when
it fails. The root filesystem the VM is made from is kept in `$WORK/cache`. `DEBS` names
another directory of packages, `E2E_ALLOW_TCG=1` runs without KVM (far too slowly to be
useful). The VM's disk is about 6 GB and is left behind.

The passphrase of the VM is a fixture, `synctang-test-passphrase`, of a disk that holds
nothing; it appears in the test's command lines, so do not point this at a real machine.

## In CI

`.github/workflows/e2e.yml` runs it on a runner with KVM, for every pull request and
for pushes to `main` that touch what it tests, and uploads the logs. On a `v<versionCode>` tag,
`ci.yml` calls it on the packages that `build-deb` made, and the release job waits for it,
so a release is only made of packages that unlocked a machine. `[U]` Neither workflow
has run on GitHub yet: the KVM step is the one `emulator.yml` uses, and the rest
was written to match what ran here.

The `go-test` job in `ci.yml` also builds the two servers (`scripts/build-syncthing-servers.sh`)
and gives them to `go test`, so that the tests in `integration/` run instead of
skipping. `[V]` here: five run and pass in about 85 seconds, five more are opt-in
measurements and skip. One of the five, the relay pool case, failed once in thirteen
runs with "already connected" before this (an open question, not fixed), so a red CI run
there is not necessarily a regression.
