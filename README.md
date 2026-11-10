<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# synctang

synctang unlocks an encrypted disk (LUKS) when a person approves.

A machine asks for the key. A key holder answers. A key holder is a phone or a laptop.
No server runs all the time. The machine does not restart without a person.
Use Tang if you need unattended restarts.

The LUKS passphrase is never on the phone. The phone keeps a different secret. A key in the
phone secure hardware wraps that secret. The key opens only for your fingerprint.

This software is a work in progress. `STATUS.md` shows what is done.
There is no compatibility promise between releases. After an upgrade, pair the phone again.

## Parts

- `synctang-unlocker` is the machine side. It runs in the dracut initrd and asks for the key.
  On the running system, it pairs and enrols key holders.
- `synctang-keyholder` is the laptop program.
- The Android app in `android/` is the phone key holder.
- `dracut/90synctang` is the dracut module that starts the agent at boot.

## Security

Warning: One approval gives the volume secret to the sender of the request.
A copy of your disk can send a request that looks like the real one.
Approve only when you restart the machine yourself.
If you suspect that someone has a copy of the disk, change the LUKS passphrase.
Then enrol the key holders again.

All traffic uses TLS 1.3. Each side checks the Device ID of the other side.
Relay and discovery servers see two Device IDs and the amount of traffic. They see nothing else.

synctang does not protect you in these cases:

- A person approves a request that they should refuse.
- Someone has a copy of the disk. A TPM can seal the machine identity, but this is not built.
- The machine restarts without a person. This is by design.

Read `docs/threat-model.md` for the details.

## Install

### Machine and laptop (Debian and Ubuntu)

Warning: `synctang-dracut` replaces the initrd of each kernel with an initrd that dracut builds.
Debian and Ubuntu use initramfs-tools by default. We did not test this on a standard installation.
Test it first on a machine that you can repair. The console passphrase prompt still works
if the agent fails.

1. Get the packages. A release on GitHub has them for amd64 and arm64.
   Each file has a `SHA256SUMS` entry, an attestation and a cosign bundle.
   To build them, run `just deb`.
2. On the machine, install the unlocker and the dracut module:

        sudo apt install ./dist/synctang-unlocker_*.deb ./dist/synctang-dracut_*.deb

3. On the laptop, install the key holder:

        sudo apt install ./dist/synctang-keyholder_*.deb

To verify a download, read `docs/releasing.md`.

### Other systems

1. Build the programs: `go build ./cmd/synctang-unlocker ./cmd/synctang-keyholder`.
2. On the machine, install `synctang-unlocker` in the path.
3. Run `just install-dracut`. Then run `dracut --force`.

On Fedora and RHEL, install `dracut-network` first. Without it, dracut stops with
"Module 'synctang' depends on module 'network'".

### Phone

1. Install an F-Droid client.
2. Add the repository `https://muelli.github.io/synctang/fdroid/repo`.
3. Install "synctang key holder".

A release APK and a debug APK have different signatures. Android does not update from one to the other.
If you uninstall the app, the phone key is lost. Then pair the phone again.

## Quick start

Run the machine commands as root on the machine. Use `sudo -i`. A process substitution
does not work with a plain `sudo`.

First, put the LUKS passphrase in a shell variable. Do not write it to a file.

    read -rsp 'LUKS passphrase: ' PASS

To find the device, run `synctang-unlocker devices`.

### 1. Pair a phone

On the machine:

    synctang-unlocker pair --device /dev/sdXN --name my-machine \
      --existing-passphrase-file <(printf %s "$PASS")

The command shows a QR code. Scan it with the app. The machine shows the key holder that answered.
The app shows a code. Compare it with the code on the machine. If the codes are the same, enter `y`.
If the codes are different, enter `n`. Another person used the link first.

The code is valid for 5 minutes. It enrols one key holder.

### 2. Pair a laptop

On the machine, run the same command. It prints a second command under the QR code. Copy that
command. Run it on the laptop. Keep the quotes.

    synctang-keyholder pair 'synctang://pair?machine=...&name=my-machine&psk=...'

The laptop and the machine show a 6-digit code. Enter `y` on the machine only if the codes are the same.
The laptop remembers the machine under its name.

### 3. Check before the first restart

    sudo synctang-unlocker rehearse

The command checks the initrd and waits for a key holder. It does not restart the machine.
If it finds a problem, it shows the fix. For example, it can tell you to run `dracut --force`.

### 4. Unlock

At boot, the agent starts with the normal console prompt. The first answer wins.
You can always type the passphrase on the console.

On the phone: tap Unlock and touch the fingerprint sensor. On the laptop:

    synctang-keyholder unlock my-machine

If the laptop knows only one machine, use `synctang-keyholder unlock`.

## More

- `docs/usage.md` has the full detail. It covers options, the phone app and kernel arguments.
  It also covers your own relay and discovery servers, external drives, and how to revoke or replace a key holder.
- The manual pages describe each command: `man synctang-unlocker`, `man synctang-keyholder`
  and `man synctang-network.conf`.
- `docs/threat-model.md` describes the threats. `docs/mr1-protocol.md` describes the protocol.
- `STATUS.md` shows what works and what does not.

At a terminal, the programs use colour and symbols. Use `--color=never` or `NO_COLOR=1` to
stop this. In a pipe, the output is plain text.

## Licence

AGPL-3.0-or-later. Read `LICENSE`. `THIRD_PARTY.md` lists the dependencies.
`docs/releasing.md` explains how releases are built.
