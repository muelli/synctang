<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# synctang

An on-demand LUKS volume unlocker for dracut-based boots. A machine asks;
a human, on a laptop or phone, answers. There is no always-on server, and
unattended reboots are deliberately not supported: if you need those, use
Tang. In exchange the key holder's secret can be kept on a phone wrapped by a key in
its secure hardware, which opens it only for your fingerprint, once per
unlock (the secret is briefly in the app's memory while it is used; the app says
plainly where the hardware half lives), or on a laptop in a file or a TPM, gated behind a keypress, and the LUKS
passphrase is never on the phone in any form.

Work in progress; see `STATUS.md` for what is built and what is not.

## What is here

- `synctang-unlocker`: the machine side. Runs in the dracut initrd as a systemd
  password agent, and on the booted system to enrol and revoke key
  holders. Subcommands: `pair`, `enrol`, `status`, `agent`.
- `synctang-keyholder`: the laptop CLI. Subcommands: `init`, `export-pubkey`,
  `unlock`.
- The Android app (`android/`): the phone key holder. Pairs by scanning a
  QR code; unlocks behind a biometric prompt. Its key is kept wrapped by a
  key in the Android Keystore that opens only for a fingerprint, once for each
  unlock.
- `dracut/90synctang`: the dracut module that starts the agent at boot.

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

**Machine and laptop.** Install the Debian packages you need
(`synctang-dracut` pulls in `synctang-unlocker` and rebuilds your initrds). A
release has them, for amd64 and arm64, among its assets on GitHub, with a
`SHA256SUMS` file, a build-provenance attestation for each
(`gh attestation verify <file> --repo muelli/synctang`), and a cosign bundle
next to each file (see `docs/releasing.md`). Or build them:

    just deb
    sudo apt install ./dist/synctang-unlocker_*.deb ./dist/synctang-dracut_*.deb   # the machine
    sudo apt install ./dist/synctang-keyholder_*.deb                                # a laptop key holder

Without Debian packaging, `go build ./cmd/synctang-unlocker ./cmd/synctang-keyholder`,
install the binary as `synctang-unlocker` on the machine, run `just install-dracut`,
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

Every command, flag, file and kernel argument is in the manual pages:
`man synctang-unlocker` (the machine), `man synctang-keyholder` (the laptop) and
`man synctang-network.conf` (choosing the relay and discovery servers). The Debian
packages install them; from a checkout, `man -l man/synctang-unlocker.8`.

The commands below need root on the machine. `--existing-passphrase-file`
takes an existing LUKS passphrase; the examples use process substitution
so it never touches a disk. (`-` reads it from stdin, but `pair` also
uses stdin for its confirmation question.) Run the commands from a root shell
(`sudo -i`): `sudo synctang-unlocker ... <(printf %s "$PASS")` fails with `open
/dev/fd/63: no such file or directory`, because sudo closes the descriptor that
makes the process substitution work. For a single command,
`printf %s "$PASS" | sudo synctang-unlocker enrol ... --existing-passphrase-file -`
does work (not for `pair`).

    read -rsp 'LUKS passphrase: ' PASS

The first `pair` or `enrol` creates the machine's network identity in
`/etc/synctang/machine.pem`. Run `dracut --force` after it, so the initrd
carries that identity; the token itself is read from the disk at boot.

### Pair a phone

    synctang-unlocker pair --device /dev/sdXN --name my-machine \
      --existing-passphrase-file <(printf %s "$PASS")

Tests the passphrase against the header first (a typo stops it before anything
is shown or created), then shows a QR code and a one-time code and waits. Scan it in the app: the
phone proves it holds the code and sends its public key, and the machine
shows you which key holder answered before it writes anything. The code
lasts five minutes (`--timeout`), enrols one key holder, and is useless
replayed or on a second phone. `--yes` skips the confirmation. If the QR
code will not scan (a serial console, say), the link it carries is
printed as text to paste into the app's device ID field. (A device ID
typed on its own only remembers the machine; the link, with its one-time
code, is what enrols the phone.)

There is one enrolled key holder per transport ID. If the phone that answers is
already enrolled with the same key, nothing is asked and nothing is added
(`already enrolled at keyslot N`). If it is enrolled with a different key, which
is what happens when the app was updated and made a new key but kept its
transport ID, the machine asks `replace the existing entry for this key holder
(keyslot N, key X) with this one (key Y)? [y/N]`; on `y` the new key is enrolled
first and the old one revoked after it, so there is never a moment with neither,
and `--yes` answers yes. So to re-pair a phone whose key changed, run the same
`pair` command again and answer `y`. `enrol` does the same for a laptop: the same
key is a no-op, and a different key for an enrolled ID is refused with the
`--replace` command to use instead.

`status` warns if one transport ID is enrolled more than once (a state older
versions could leave behind), and the agent then refuses to answer for it rather
than pick a key. Repair it by naming the stale keyslot:

    synctang-unlocker enrol --device /dev/sdXN --remove <transport id> --keyslot <n> \
      --existing-passphrase-file <(printf %s "$PASS")

The phone waits up to five minutes (the machine's default `--timeout`) for you to
answer that question, separately from the time it allows to reach the machine; if
it runs out it says the machine did not confirm, and pairing again says the phone
is already enrolled if you confirmed just after.

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

    synctang-keyholder init
    synctang-keyholder export-pubkey

On the machine, with the two values that printed:

    synctang-unlocker enrol --device /dev/sdXN --name my-laptop \
      --pubkey <public key> --recipient-transport-id <transport id> \
      --existing-passphrase-file <(printf %s "$PASS")

This adds a LUKS2 keyslot holding a fresh secret and an `mr-1` token
entry for that key holder, and prints the machine's own transport ID,
which the laptop needs to dial it. (`synctang-keyholder pair` is not built yet.)

### See who can unlock

    synctang-unlocker status --device /dev/sdXN

Prints the machine's transport ID and name and, for each enrolled key
holder, its transport ID, keyslot and a short key id. Read-only. Run it
before a revocation: naming the wrong transport ID revokes the wrong key
holder.

### Unlock

At boot the agent starts alongside the ordinary console prompt; whoever
answers first wins, so the console keeps working. To unlock, tap Unlock in
the app, or on the laptop:

    synctang-keyholder unlock <machine transport id>

The key holder shows the machine's name, waits for Enter (or `--yes`),
and answers once. The machine checks the recovered secret actually opens the volume
before handing it to systemd, and tells the key holder how it went.
The app gives you 75 seconds to approve, counted from when it connected to the machine,
and shows how long is left on the approve screen (a connection with nothing on it can be
closed by a relay or a network after a minute or two; `docs/relay-idle.md`). The 75
seconds include the fingerprint prompt: if it is still up when they run out, the app
dismisses it, tells the machine no and says so, and a touch that arrives after that
answers nothing.
While the app connects, pairs or sends its answer, it says what it is doing (looking
for the machine, connected through a relay or on the local network and waiting for the
machine to ask, waiting for you to confirm on the machine), how long it has been, and
which attempt it is on after the first.

The app's main screen shows a dot and a line of words saying whether the machine has announced
itself to the discovery server lately (waiting, announced some minutes ago, not announced, or
cannot check); it is what the machine published, not a promise that an unlock will work, and it
never stops you pressing Unlock. A machine that is waiting republishes every minute, so the
app looks once when the screen appears or the app comes back, then every 30 seconds or so, and
"Check again" looks now (at most every 10 seconds). The public discovery server is shared and
answers a client that asks too often with HTTP 429 and a Retry-After of minutes: the app then
leaves it alone for that long and keeps showing what it showed, and a dial's retries (and the
command-line tools') back off from 3 to 15 seconds and wait out a Retry-After too.
When an unlock gives up, the app (and `synctang-keyholder unlock`) looks the machine up once more and
says what discovery knows: it announced itself N seconds ago but did not answer, it last announced
itself minutes or hours ago so it may not be at its prompt, or it never announced itself.
A debug build of the app logs what it is doing, and `adb logcat -v time -s synctang GoLog`
follows it; release builds log warnings only (`docs/debugging.md`).

Kernel command line options:

- `rd.synctang=0` does not start the agent; the escape hatch for when the
  network path is what is broken.
- `rd.synctang.log=LEVEL` sets how much the agent logs (`trace`, `debug`, `info`, `warn` or
  `error`, default `info`). Its log goes to the journal, so after the boot
  `journalctl -b -t synctang-unlocker` shows it.
- `rd.synctang.relay=URL` and `rd.synctang.discovery=URL` use your own relay
  and discovery server instead of the public ones, for this boot; see
  "Use your own relay and discovery server" below.

Off the local network the phone needs Internet access; on the same
network it does not. Both sides try local discovery (a multicast on the
LAN, IPv4 only, same broadcast domain) and the public Syncthing relay
network at once, and use whichever finds the other first. Details in
`docs/transport.md`.

### Use your own relay and discovery server

The public Syncthing relay pool and discovery servers are the default. To use
your own, on the machine put them in `/etc/synctang/network.conf` and rebuild
the initrd (for example `sudo dracut --force`):

    relay-pool=relay://192.0.2.10:22067/?id=<the relay's Device ID>
    discovery=https://discovery.example.org/v2/

`relay-pool` is either a relay pool URL or, as here, one relay of your own;
`discovery-announce=` is only needed when announcements go to a different
URL than lookups. Only the machine needs a relay setting: a key holder learns the relay from
discovery. `synctang-unlocker pair` then puts the discovery server in the pairing
link, so a phone paired by QR code follows the machine's choice and nobody
types a URL on it (update the app first: an older one ignores it and looks
on the public servers). On a laptop, give the discovery server with
`synctang-keyholder unlock --discovery URL`, or in `~/.config/synctang-keyholder/config`. Both
sides must use the same discovery server. A private discovery server with a
self-signed certificate is trusted by its Device ID, written in the URL as
`?id=<Device ID>` (or `?insecure` to skip the check); with neither, the
system's certificate authorities decide. Plain `http://` is for lookups only,
never for announcing. A relay or discovery server sees
two Device IDs and how much traffic passed, not what it carried. Details,
and what was and was not tested, in `docs/private-servers.md`.

### Open a drive after boot

The agent only runs in the initrd, and is stopped before the switch to the
real root. For a volume that is not asking for a password at boot, an
external drive plugged in later say, there is a command that waits for a key
holder and then opens the volume itself:

    sudo synctang-unlocker open /dev/sdb1 backup

It prints this machine's transport id, waits (five minutes, `--timeout`) for
a key holder to approve, checks that the recovered secret really opens the
volume, runs `cryptsetup open`, prints `/dev/mapper/backup` and exits.
Nothing keeps running. Without a name the volume is called
`synctang-<start of its UUID>`. Mounting is a separate step, and
`cryptsetup close backup` closes it.

The drive has to have a key holder enrolled first, which writes a keyslot and a
token into the drive's own LUKS header: run `synctang-unlocker pair --device /dev/sdb1`
(or `enrol`) once, with the drive's passphrase, as for any other volume, and
back its header up first. The token names key holders, not a machine, so the
drive can be opened on any computer that runs `synctang-unlocker` and whose transport
id the key holder knows. The phone app keeps one paired machine at a time; the
laptop's `synctang-keyholder unlock <transport id>` takes any.

### Keyboard use of the app

The app can be driven from a hardware keyboard (Android's desktop mode, or a
phone with one attached). Tab and Shift+Tab move between controls, Enter or
Space presses the focused one, and each screen starts with its main control
focused. Every control also has a key, which works wherever focus is; Meta+/
shows the ones the current screen offers.

| Key | Does |
| --- | --- |
| Ctrl+Enter | The main action of the screen: Unlock, Approve and unlock, Try again, Scan again, Pair, Unlock something again |
| Esc | Back out, as the Back gesture does: Decline, Cancel, keep the machine already paired, close About, dismiss the undo bar |
| Ctrl+P | Pair with a machine again |
| Ctrl+R | Check again whether the machine is waiting |
| Ctrl+I | About this phone |
| Ctrl+E | Enrol by hand |
| Ctrl+Shift+C | Copy the enrolment command |
| Ctrl+T | Show or hide the technical details of an error |
| Ctrl+1, Ctrl+2 | Show in full or less: the device ID, the key requested |
| Ctrl+G, Ctrl+O | Allow the camera, open the app's settings |
| Ctrl+Z | Undo a pairing that replaced a machine |

"Use a software key on this phone" has no key, and is not focused when its
screen opens: it weakens a protection, so it has to be reached and pressed on
purpose. A held key does not repeat an action. Each screen names itself to the
screen reader and in the window title, for instance "synctang: Pair with a
machine".

### Revoke or rotate a key holder

Revoke destroys that key holder's keyslot and removes its token entry,
leaving the others alone:

    synctang-unlocker enrol --device /dev/sdXN --remove <transport id> \
      --existing-passphrase-file <(printf %s "$PASS")

Rotate replaces a key holder's key in one command. The new key is enrolled
before the old one is revoked, so it can open the volume throughout:

    synctang-keyholder init --force        # on the laptop: new key, same transport id
    synctang-keyholder export-pubkey
    synctang-unlocker enrol --device /dev/sdXN --replace <transport id> --pubkey <new public key> \
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
