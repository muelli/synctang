<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Using synctang in detail

This guide holds the full detail that the README leaves out: the options of each command, the
phone app, the kernel arguments, private servers and the revoke and rotate steps. The README has
the short version. Every command, flag and file is also in the manual pages
(`man synctang-unlocker`, `man synctang-keyholder`, `man synctang-network.conf`).

The commands below need root on the machine. `--existing-passphrase-file`
takes an existing LUKS passphrase; the examples use process substitution
so it never touches a disk. Run the commands from a root shell
(`sudo -i`): `sudo synctang-unlocker ... <(printf %s "$PASS")` fails with `open
/dev/fd/63: no such file or directory`, because sudo closes the descriptor that
makes the process substitution work. For a single command, `-` reads the
passphrase from stdin (`--existing-passphrase-file -` or `--existing-passphrase-file=-`):

    printf %s "$PASS" | sudo synctang-unlocker enrol ... --existing-passphrase-file -

`pair` takes it the same way. Its confirmation question then cannot be answered on
stdin, which the passphrase has used up, so it is asked on the terminal; with no
terminal (from a script, say) pass `--yes`, and `pair` says so at once, before it
shows a code.

    read -rsp 'LUKS passphrase: ' PASS

At a terminal both programs colour their output (errors red, warnings yellow, what went well green), mark
lines with check marks and crosses, show tables with headers, frame the code you compare when pairing and turn
a spinner while they wait. Prose is wrapped to the width of the terminal. `--color=never` (anywhere on the
line) or `NO_COLOR=1` turns the colour off, `--color=always` keeps it in a pager, and a locale that is not
UTF-8 gets plain words instead of symbols. To a pipe or a file the output is plain text, as scripts read it.
`synctang-keyholder unlock` with no machine named unlocks the only one it knows, and a mistyped command or
machine name is answered with "did you mean ...?".

Shell completion for bash, zsh and fish comes with the Debian packages. Both programs
write it themselves from their own commands and flags (`synctang-unlocker completion bash`,
and likewise for `zsh` and `fish`), so it cannot name an option the program does not have;
without the package, `source <(synctang-unlocker completion bash)` in `~/.bashrc` does it.
Options are spelled with two dashes, as in the manual pages (`--device`), and a single dash
also works.

Not sure which device holds the volume? `synctang-unlocker devices` (as root) lists the
LUKS devices on the machine with their UUID, size, the name each is open under, and how
many key holders are enrolled; `enrol` and `pair` print the same list when `--device` is
left out.

    synctang-unlocker devices
    DEVICE     UUID                                  SIZE     OPEN AS              SYNCTANG
    /dev/vda3  91ae9308-22e5-4eba-af83-cc26c3c87818  5.0 GiB  synctang_test_crypt  2 key holders

The first `pair` or `enrol` creates the machine's network identity in
`/etc/synctang/machine.pem`. The initrd has to carry that identity, or at boot the agent
makes a new one that no key holder knows and the machine is not found; the enrolled key
holders themselves are read from the disk at boot, so only a change of identity or of
`network.conf` needs a rebuild. An initrd built before the identity existed, which is the
case right after installing the packages, does not have it. `pair` and `enrol` check
this when they finish and say so, in words, with the fix; add `--rebuild-initrd` and they
run `dracut --force` themselves when it is needed. `synctang-unlocker status` shows the
same check at any time (it needs root, and `lsinitrd`, which dracut brings):

    initrd: carries this machine's identity
    initrd: does not contain this machine's identity (/etc/synctang/machine.pem), so at boot ...

It looks at the initrd of the running kernel, which is the one `dracut --force` rebuilds.
A rebuild rewrites your `/boot` images: keep a copy of the current one if the machine is
not within reach.

## Pair a phone

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

## Pair a laptop

Run the same command as for a phone on the machine:

    synctang-unlocker pair --device /dev/sdXN --name my-machine \
      --existing-passphrase-file <(printf %s "$PASS")

Under the QR code it prints a second command, with the link in it. On the laptop, run that (the quotes
matter, the link contains `&`):

    synctang-keyholder pair 'synctang://pair?machine=...&name=my-machine&psk=...'

That one string is all that is copied. The laptop makes its key if it has none, proves it holds the
one-time code, and sends its public key. **Both sides now show a six-digit code, and the person at the
machine is asked whether to enrol: say yes only if the code on the laptop is the same.** The one-time
code only proves that whoever answered had it, and a link that was photographed, read over a shoulder
or pasted somewhere is had by more than one party. If the codes differ, answer `n` on the machine and
pair again for a new link. (The phone app shows the same code while it waits, and says what to do if it differs.)

The laptop remembers the machine under the name in the link, so unlocking is:

    synctang-keyholder unlock my-machine

`synctang-keyholder machines` lists the names, and `machines add <name> <transport id>` names a machine
by hand. The names live in a plain text file, `~/.config/synctang-keyholder/machines`.

### By hand

`enrol` is the low-level way, for what pairing cannot reach (a machine that cannot be reached over the
network while you pair, an image, a script). On the laptop:

    synctang-keyholder init

It prints the public key and the transport ID, and the command below with them filled in
(`synctang-keyholder export-pubkey` prints the two values again later). On the machine, with the two
values that printed (both are required):

    synctang-unlocker enrol --device /dev/sdXN --name my-laptop \
      --pubkey <public key> --recipient-transport-id <transport id> \
      --existing-passphrase-file <(printf %s "$PASS")

This adds a LUKS2 keyslot holding a fresh secret and an `mr-1` token
entry for that key holder, and prints the machine's own transport ID,
which the laptop needs to dial it. Name it with `synctang-keyholder machines add`.

## Where the laptop's key is kept

`synctang-keyholder init` keeps the key in GNOME Keyring (through the Secret Service) when there is one, and in a plain file, with a warning that says why, when there
is not. `--store` chooses: `file`, `secret-service`, `systemd-creds` (sealed by systemd for your account,
no desktop needed, systemd 256 or later) or `systemd-creds:tpm2` (also sealed to this machine's TPM, so
the key cannot be moved or backed up from the blob; written for a machine with a TPM and not yet run on one). A keyring item is bound to the absolute path of the key file, so a moved key directory leaves it behind
(`status` then says there is no key at that path). A store you ask for by name is never replaced by
another if it is not there: the command fails. `synctang-keyholder status` says which store holds the key,
what that does and does not protect against (`threat-model.md` has the table), and whether it reads.

None of the stores stops
software running as you from asking for the key.

To see where a command reads the key from, add `--verbose` (or set `SYNCTANG_DEBUG=1`). The debug log goes to
standard error. It names the store and the place (a path, or the Secret Service item and its account) and says
whether the key matched the public key recorded when it was made. It never prints the key. A key that a store
hands back and that is not the one that was made is refused, with a message that says so.

## See who can unlock

    synctang-unlocker status --device /dev/sdXN

Prints the machine's transport ID and name and, for each enrolled key
holder, its transport ID, keyslot and a short key id. Read-only. Run it
before a revocation: naming the wrong transport ID revokes the wrong key
holder.

## Rehearse the first reboot

Before the first reboot of a machine that was just set up, run

    sudo synctang-unlocker rehearse

(`pair` suggests it when it is done). It checks what the boot needs: that the initrd of the
running kernel carries this machine's identity and `network.conf`, has the `synctang` and
`network` dracut modules, holds the same `synctang-unlocker` that is installed (an upgrade that
did not rebuild the initrd leaves the old one in it), and that the kernel command line does not
carry `rd.synctang=0`. Each is `ok`, `PROBLEM` with the fix, or `unchecked` with the reason. Then
it waits for a key holder, as `open` does, through the relay and discovery servers the machine
would use at boot; you approve on the phone or the laptop; and it checks that what the machine
recovered opens the volume. Nothing is opened: the volume of a running machine is in use, and the
check is the one the agent makes at boot. `--initrd-only` does the first part alone.

A pass means a key holder is paired against this machine's real identity, the network settings work
from outside, and its key opens the volume. It cannot test the network in early boot (DHCP in the
initrd) or that the initrd starts, only a reboot does; if either fails the machine still shows its
ordinary passphrase prompt on the console, and the passphrase still works.

## Unlock

At boot the agent starts alongside the ordinary console prompt; whoever
answers first wins, so the console keeps working. To unlock, tap Unlock in
the app, or on the laptop:

    synctang-keyholder unlock <machine transport id>

The key holder shows the machine's name, waits for Enter (or `--yes`),
and answers once. The machine checks the recovered secret actually opens the volume
before handing it to systemd, and tells the key holder how it went.
In the app, Unlock is one tap: it connects, and the fingerprint prompt comes up by itself as
soon as the machine's request arrives, naming the machine. If you cancel the prompt, the
approve screen (the machine's name and key, and how long is left) is there to try again
or to decline; the machine is told nothing until you decline or the time runs out.
The app gives you 75 seconds to approve, counted from when it connected to the machine,
and shows how long is left on the approve screen (a connection with nothing on it can be
closed by a relay or a network after a minute or two; `relay-idle.md`). The 75
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
app looks once when the screen appears or the app comes back, then every minute or so, and
"Check again", or a long press on the dot, looks now (at most every 10 seconds). The public discovery server is shared and
answers a client that asks too often with HTTP 429 and a Retry-After of minutes: the app then
leaves it alone for that long and keeps showing what it showed, and a dial's retries (and the
command-line tools') back off from 3 to 15 seconds and wait out a Retry-After too.
When an unlock gives up, the app (and `synctang-keyholder unlock`) looks the machine up once more and
says what discovery knows: it announced itself N seconds ago but did not answer, it last announced
itself minutes or hours ago so it may not be at its prompt, or it never announced itself.
A debug build of the app logs what it is doing, and `adb logcat -v time -s synctang GoLog`
follows it; release builds log warnings only (`debugging.md`).

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
`transport.md`.

## Use your own relay and discovery server

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
types a URL on it. On a laptop, give the discovery server with
`synctang-keyholder unlock --discovery URL`, or in `~/.config/synctang-keyholder/config`. Both
sides must use the same discovery server. A private discovery server with a
self-signed certificate is trusted by its Device ID, written in the URL as
`?id=<Device ID>` (or `?insecure` to skip the check); with neither, the
system's certificate authorities decide. Plain `http://` is for lookups only,
never for announcing. A relay or discovery server sees
two Device IDs and how much traffic passed, not what it carried. Details,
and what was and was not tested, in `private-servers.md`.

## Open a drive after boot

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

## Keyboard use of the app

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

## Revoke or rotate a key holder

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
