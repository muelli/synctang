# synctang

An on-demand LUKS volume unlocker for dracut-based boots. A machine asks;
a human, on a laptop or phone, answers. There is no always-on server, and
unattended reboots are deliberately not supported: if you need those, use
Tang. In exchange the key holder's secret can live in a phone's StrongBox
or a laptop's TPM, gated behind a biometric or a keypress, and the LUKS
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
disk. They are useless without a key holder, but a thief can still *ask*,
so:

- The key holder shows the machine's name and identity and waits for you
  to approve every unlock.
- `--confirm-code` makes the machine print a code on its own console that
  you must read back, so a thief without eyes on the console cannot
  complete an unlock.
- Everything crosses TLS 1.3 with the peer pinned by Device ID; rendezvous
  servers and relays see two Device IDs and a traffic volume, nothing else.

Not defended against: a coerced or careless human pressing approve, and
unattended reboots by design. Details in `docs/threat-model.md`; the
protocol is in `docs/mr1-protocol.md`.

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
code will not scan (a serial console, say), the same two values are
printed as text to type into the app. `NO_COLOR` gives a plain ASCII code.

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
sends the confirm code back if the machine asked for one, and answers
once. The machine checks the recovered secret actually opens the volume
before handing it to systemd, and tells the key holder how it went.

Kernel command line options:

- `rd.unlocker=0` does not start the agent; the escape hatch for when the
  network path is what is broken.
- `rd.unlocker.confirm_code=1` starts it in confirm-code mode. Off by
  default because it needs a human at the console; opt in per machine.

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

To forget a machine on the phone, clear the app's data in Android's
settings, then revoke the phone on the machine.

## Licence

AGPL-3.0-or-later. See `LICENSE`. Dependencies are listed in
`THIRD_PARTY.md`; how releases are built and published is in
`docs/releasing.md`.
