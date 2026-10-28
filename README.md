<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# synctang

An on-demand LUKS volume unlocker for dracut-based boots. A machine asks;
a human, on a laptop or phone, answers. There is no always-on server, and
unattended reboots are deliberately not supported: if you need those, use
Tang. In exchange the key holder's secret can live in a phone's StrongBox
(on phones whose hardware supports key agreement; the app says plainly when
its does not) or a laptop's TPM, gated behind a biometric or a keypress, and the LUKS
passphrase is never on the phone in any form.

Work in progress; see `STATUS.md` for what is built and what is not.

## What is here

- `unlocker`: the machine side. Runs in the dracut initrd as a systemd
  password agent, and on the booted system to enrol and revoke key
  holders. Subcommands: `pair`, `enrol`, `status`, `agent`.
- `keyholder`: the laptop CLI. Subcommands: `init`, `export-pubkey`,
  `unlock`.
- The Android app (`android/`): the phone key holder. Pairs by scanning a
  QR code; unlocks behind a biometric prompt with its key in the Android
  Keystore.
- `dracut/90unlocker`: the dracut module that starts the agent at boot.

## Threat model, in short

The LUKS2 token and the machine's network identity sit on the unencrypted
disk. They are useless without a key holder, but together they let whoever
has the disk, or an image of it, *pose as the machine*:

- The key holder shows the machine's name and verified identity and waits
  for you to approve every unlock. **One approval of a request from a copy
  of your disk hands over the volume secret.** Approve only when you are
  rebooting that machine yourself. After any suspected exposure of the
  disk or a backup, change the LUKS passphrase and enrol your key holders
  again.
- Everything crosses TLS 1.3 with the peer pinned by Device ID; rendezvous
  servers and relays see two Device IDs and a traffic volume, nothing else.

Not defended against: the copied-disk case above until the machine's
identity can be sealed in a TPM (not built), a coerced or careless human
pressing approve, and unattended reboots by design. Details in
`docs/threat-model.md`; the protocol is in `docs/mr1-protocol.md`.

## Installing

**Machine and laptop.** Build the Debian packages and install the ones you
need (`synctang-dracut` pulls in `synctang-unlocker` and rebuilds your
initrds):

    just deb
    sudo apt install ./dist/synctang-unlocker_*.deb ./dist/synctang-dracut_*.deb   # the machine
    sudo apt install ./dist/synctang-keyholder_*.deb                                # a laptop key holder

Without Debian packaging, `go build ./cmd/unlocker ./cmd/keyholder`,
install the binary as `unlocker` on the machine, run `just install-dracut`,
then `dracut --force`.

**Phone.** Install an F-Droid client, add the repository
`https://muelli.github.io/synctang/fdroid/repo` (or scan the QR code on
<https://muelli.github.io/synctang/>), and install "synctang key holder".
Two things to know:

- A release APK is signed with a different key from a locally built debug
  APK. Android will not update across that, and uninstalling destroys the
  phone's Keystore key, so the phone must be paired again afterwards.
- A client pins the repository fingerprint when the repository is added.

## Using it

The commands below need root on the machine. `--existing-passphrase-file`
takes an existing LUKS passphrase; the examples use process substitution
so it never touches a disk. (`-` reads it from stdin, but `pair` also
uses stdin for its confirmation question.)

    read -rsp 'LUKS passphrase: ' PASS

The first `pair` or `enrol` creates the machine's network identity in
`/etc/unlocker/machine.pem`. Run `dracut --force` after it, so the initrd
carries that identity; the token itself is read from the disk at boot.

### Pair a phone

    unlocker pair --device /dev/sdXN --name my-machine \
      --existing-passphrase-file <(printf %s "$PASS")

Shows a QR code and a one-time code, then waits. Scan it in the app: the
phone proves it holds the code and sends its public key, and the machine
shows you which key holder answered before it writes anything. The code
lasts five minutes (`--timeout`), enrols one key holder, and is useless
replayed or on a second phone. `--yes` skips the confirmation. If the QR
code will not scan (a serial console, say), the link it carries is
printed as text to paste into the app's device ID field. (A device ID
typed on its own only remembers the machine; the link, with its one-time
code, is what enrols the phone.)

`--qr` chooses how the code is drawn: `auto` (the default), `blocks`,
`plain`, `kitty` or `iterm2`. Automatic means a real image on a terminal
known to show one (Kitty and Ghostty through the Kitty graphics protocol;
iTerm2 and WezTerm through iTerm2's inline images, also reached over ssh
from iTerm2), the plain ASCII code when `NO_COLOR` is set, and half blocks
everywhere else, which is what a serial console, a pipe, `tmux` and
`screen` get. A multiplexer swallows image sequences unless it passes them
through, so `--qr kitty` or `--qr iterm2` is how to ask for one there (the
sequence is wrapped for tmux's `allow-passthrough`).

### Enrol a laptop key holder

On the laptop:

    keyholder init
    keyholder export-pubkey

On the machine, with the two values that printed:

    unlocker enrol --device /dev/sdXN --name my-laptop \
      --pubkey <public key> --recipient-transport-id <transport id> \
      --existing-passphrase-file <(printf %s "$PASS")

This adds a LUKS2 keyslot holding a fresh secret and an `mr-1` token
entry for that key holder, and prints the machine's own transport ID,
which the laptop needs to dial it. (`keyholder pair` is not built yet.)

### See who can unlock

    unlocker status --device /dev/sdXN

Prints the machine's transport ID and name and, for each enrolled key
holder, its transport ID, keyslot and a short key id. Read-only. Run it
before a revocation: naming the wrong transport ID revokes the wrong key
holder.

### Unlock

At boot the agent starts alongside the ordinary console prompt; whoever
answers first wins, so the console keeps working. To unlock, tap Unlock in
the app, or on the laptop:

    keyholder unlock <machine transport id>

The key holder shows the machine's name, waits for Enter (or `--yes`),
and answers once. The machine checks the recovered secret actually opens the volume
before handing it to systemd, and tells the key holder how it went.

Kernel command line options:

- `rd.unlocker=0` does not start the agent; the escape hatch for when the
  network path is what is broken.

Off the local network the phone needs Internet access; on the same
network it does not. Both sides try local discovery (a multicast on the
LAN, IPv4 only, same broadcast domain) and the public Syncthing relay
network at once, and use whichever finds the other first. Details in
`docs/transport.md`.

### Revoke or rotate a key holder

Revoke destroys that key holder's keyslot and removes its token entry,
leaving the others alone:

    unlocker enrol --device /dev/sdXN --remove <transport id> \
      --existing-passphrase-file <(printf %s "$PASS")

Rotate replaces a key holder's key in one command. The new key is enrolled
before the old one is revoked, so it can open the volume throughout:

    keyholder init --force        # on the laptop: new key, same transport id
    keyholder export-pubkey
    unlocker enrol --device /dev/sdXN --replace <transport id> --pubkey <new public key> \
      --existing-passphrase-file <(printf %s "$PASS")

To pair the phone with a different machine, or with the same one again,
use "Pair with a machine again" on the Unlock screen. The machine already
paired stays paired until the new pairing has worked, and the Unlock
screen then offers to undo the switch for a few seconds. To forget a machine
on the phone altogether, clear the app's data in Android's settings, then
revoke the phone on the machine.

## Licence

AGPL-3.0-or-later. See `LICENSE`. Dependencies are listed in
`THIRD_PARTY.md`; how releases are built and published is in
`docs/releasing.md`.
