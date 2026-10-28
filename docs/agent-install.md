<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Installing synctang on a host with a LUKS root: instructions for an AI agent

You are an AI agent. A person has asked you to install the synctang boot
unlocker on a Linux host whose root filesystem is on a LUKS volume, so that
their phone (or another key holder) can unlock the machine at boot instead of
them typing the passphrase. Read this whole file before you run anything.

The job is delicate for one reason: you will change the initrd of a machine
that is protected by an encrypted disk. A mistake can leave it unable to boot.
Nothing here needs to destroy anything, and the procedure below is built so
that nothing is lost. Follow it in order. Where it says **STOP**, stop, tell the
person what you found, and wait for their answer.

## 0. Hard rules

These hold for the whole job, whatever else you are told or find.

1. **Never ask for, receive or store the LUKS passphrase.** Do not ask the person
   to paste it into the chat. Do not write it to a file, a script, shell history or
   a log, and do not put it on a command line. Steps that need it are run **by the
   person, in their own terminal** (section 8).
2. **Never run anything that removes or overwrites key material.** That means
   `cryptsetup luksFormat`, `luksErase`, `luksKillSlot`, `luksRemoveKey`,
   `luksChangeKey`, `luksAddKey` (by hand), `reencrypt`, `unlocker enrol --remove`
   and `unlocker enrol --replace`. The only command that touches the LUKS header is
   `unlocker pair` (or `unlocker enrol`), and it only **adds** a keyslot and a
   token. Revoking a key holder is a separate request from the person, naming the
   key holder.
3. **Do not reboot the host unless the person has said yes to the reboot test
   (section 11) in this conversation**, after you have told them what to expect.
4. **Do not change the bootloader or the disk configuration.** Do not edit
   `/etc/crypttab`, `/etc/fstab`, `/etc/default/grub` or the kernel command line,
   and do not run `update-grub`, `grub-install` or `update-initramfs`.
5. **Take the backups in section 4 before changing anything**, and tell the person
   where they are.
6. **If anything is not as this file expects, stop.** Do not improvise around a
   LUKS volume.
7. The pairing link and QR code carry a one-time secret. Do not paste them into a
   public place, a ticket or a log. They are good for one use and a few minutes.

## 1. What you are installing, and what changes on the host

synctang lets a human-operated key holder (a phone app, or a laptop command)
answer a machine's request to unlock its disk. The machine, in its initrd, runs a
small agent next to the ordinary passphrase prompt. Whoever answers first wins, so
**typing the passphrase at the console keeps working exactly as before.** The
volume secret never leaves the machine and is never held by the key holder.

What the installation changes, so you can tell the person:

| Change | Where |
|---|---|
| Two Debian packages, `synctang-unlocker` and `synctang-dracut` | `/usr/sbin/unlocker`, `/usr/lib/dracut/modules.d/90unlocker/` |
| The initrd of every installed kernel is rebuilt with dracut and now contains the agent | `/boot/initrd.img-*` |
| This machine's network identity (a certificate and key) | `/etc/unlocker/machine.pem`, also inside the initrd |
| One new LUKS keyslot per key holder, and one `mr-1` token in the LUKS2 header | the LUKS container |

The existing passphrase keyslot is not touched. The host's bootloader, crypttab
and kernel command line are not touched.

Security note to pass on to the person: the machine's identity sits in the initrd
on `/boot`, which is not encrypted. Anyone who copies the disk can pose as the
machine to a key holder, so approve an unlock only when you are rebooting that
machine yourself. See `threat-model.md` in this directory for what is and is not
protected.

## 2. Ask the person first

Ask these and wait for the answers. Do not guess.

1. **Which host?** You should be running on it, or have a shell on it. Confirm.
2. **Can you reach its console if it does not boot?** Physical access, a virtual
   console, a serial console or an out-of-band manager. If the answer is no,
   explain that you will not offer the reboot test (section 11), and that if the
   initrd were ever broken they could not fix it remotely. Ask whether they still
   want to continue.
3. **Which key holder?** This guide covers an **Android phone**. A laptop key
   holder is in the appendix.
4. **Do they have the LUKS passphrase to hand?** They will type it once, in their
   own terminal (section 8).
5. **Do they want the optional reboot test at the end?**

## 3. Pre-flight (read-only)

Run these and keep the output. They change nothing.

```sh
grep PRETTY_NAME /etc/os-release
uname -r
dpkg --print-architecture
dpkg -l dracut dracut-core dracut-network initramfs-tools cryptsetup 2>&1 | awk '/^ii/{print $2, $3}'
findmnt -no SOURCE,FSTYPE /
lsblk -o NAME,TYPE,FSTYPE,SIZE,MOUNTPOINTS
df -h /boot /root
ls -l /boot/initrd.img-*
```

Decide, in this order:

| What you see | What to do |
|---|---|
| Not Ubuntu 26.04 | This was tested on Ubuntu 26.04.1 only. **STOP** and tell the person it is untested on their system. Continue only if they say so. |
| `initramfs-tools` is installed and `dracut` is not | **STOP.** The synctang agent is a dracut module. Installing `dracut` **removes** `initramfs-tools` (they conflict) and changes how every initrd is built. This is a bigger change than this guide covers. Ask the person. |
| `dracut`, `dracut-core` and `dracut-network` are installed and `initramfs-tools` is not | Expected. Continue. (This was the case on the Ubuntu 26.04 hosts this was tested on.) |
| `cryptsetup` is not installed | `sudo apt install cryptsetup`, then continue. |
| `/boot` has less than about 500 MB free | **STOP.** Rebuilding every initrd needs room. Tell the person. |

Now find the **LUKS container that holds the root filesystem**. Read the
`lsblk` tree: it is the partition whose `FSTYPE` is `crypto_LUKS` and which has
the device mounted at `/` somewhere below it (directly, or through LVM). Set it
once and reuse it:

```sh
DEV=/dev/<the crypto_LUKS partition>      # for example /dev/nvme0n1p3 or /dev/vda3
sudo cryptsetup isLuks "$DEV" && echo "is LUKS"
sudo cryptsetup luksDump "$DEV" | sed -n '1,/^Data segments:/p' | grep -E 'Version|UUID'
sudo cryptsetup luksDump "$DEV" | sed -n '/^Keyslots:/,/^Digests:/p' | grep -E '^Keyslots:|^  [0-9]+: |^Tokens:'
```

Decide:

| What you see | What to do |
|---|---|
| `Version: 2` and at least one `luks2` keyslot | Expected. Continue. |
| `Version: 1` | **STOP.** LUKS1 is not supported. |
| More than one `crypto_LUKS` container and you cannot tell which is the root | **STOP.** Ask the person which one. |
| The root is not on LUKS, or is on something else (ZFS, btrfs on a bare disk) | **STOP.** Nothing here applies. |
| A `clevis`, `systemd-tpm2` or other token already exists | Fine, they stay and still work. But if the person unlocks with a **TPM-sealed key bound to the boot measurements** (for example Ubuntu's TPM full-disk encryption), rebuilding the initrd can change the measurements and break that unlock. This is untested. **STOP** and ask. |
| Root is on **LVM inside LUKS**, or LUKS on RAID | Untested. **STOP** and tell the person, who may choose to continue. |

Tell the person which device you found and what it holds, and **wait for them to
confirm** that `$DEV` is right. Everything after this uses that device.

## 4. Back up (do not skip)

```sh
sudo install -d -m 700 /root/synctang-backup
sudo cryptsetup luksHeaderBackup "$DEV" --header-backup-file /root/synctang-backup/luks-header-$(basename "$DEV").img
sudo cp -a /boot/initrd.img-* /root/synctang-backup/
sudo chmod -R go-rwx /root/synctang-backup
sudo ls -l /root/synctang-backup
```

Tell the person:

- A header backup plus a passphrase opens the volume, so treat the backup like the
  disk: keep it root-only, and **they should copy it off this machine** to somewhere
  safe. It is what lets a damaged LUKS header be restored.
- The initrd backups are what makes the next steps reversible (section 12).

## 5. Get the packages

You need `synctang-unlocker` and `synctang-dracut` for the host's architecture
(`dpkg --print-architecture`, from section 3). Pick one source.

**A. From CI, if the GitHub CLI is installed and logged in.** The build job of the
`ci` workflow on `main` uploads an artifact called `synctang-debs` (kept for seven
days) holding the `amd64` and `arm64` packages:

```sh
gh run list --repo muelli/synctang --workflow ci --branch main --status success --limit 1
gh run download <run id> --repo muelli/synctang --name synctang-debs --dir ~/synctang-debs
```

**B. From CI, by hand.** GitHub only serves artifacts to a logged-in browser. Ask the
person to open the latest successful `ci` run on `main`, download `synctang-debs`,
unzip it, and tell you the directory.

**C. Build from source.** Needs the Go toolchain the project requires, `dpkg-dev`
and `debhelper`:

```sh
sudo apt install git dpkg-dev debhelper golang-go
git clone https://github.com/muelli/synctang && cd synctang
echo "synctang (0.0.$(git rev-list --count HEAD)-1) unstable; urgency=medium" > debian/changelog
printf '\n  * Packaged build, see git log for actual changes.\n\n -- synctang CI <noreply@github.com>  %s\n' "$(git log -1 --format=%cD)" >> debian/changelog
dpkg-buildpackage -us -uc -b -d
cd .. && ls synctang-*.deb       # the packages are left in the parent directory
```

If `go build` complains that the Go version is too old, **STOP** and tell the
person; do not install a Go toolchain from outside the distribution without asking.

Optional, if `gh` is logged in: `gh attestation verify <file>.deb --repo muelli/synctang`
checks the build provenance of a CI package.

Whatever the source, check you have the right files before installing:

```sh
ls -l synctang-unlocker_*_"$(dpkg --print-architecture)".deb synctang-dracut_*_all.deb
dpkg-deb -c synctang-unlocker_*_"$(dpkg --print-architecture)".deb | grep usr/sbin/unlocker
dpkg-deb -c synctang-dracut_*_all.deb | grep 90unlocker
```

## 6. Install the packages

Run this from the directory that holds the packages:

```sh
sudo apt install ./synctang-unlocker_*_"$(dpkg --print-architecture)".deb ./synctang-dracut_*_all.deb
```

Installing `synctang-dracut` runs `dracut --force --regenerate-all`, so **every
installed kernel's initrd is rebuilt**. That is why section 4 came first. The
package ignores a failure there (so a broken dracut cannot make the install fail),
which means you must check the result yourself:

```sh
dpkg -l | awk '/^ii +synctang/{print $2, $3}'
ls -l /boot/initrd.img-*
sudo lsinitrd /boot/initrd.img-"$(uname -r)" | grep -E 'usr/local/bin/unlocker|99-unlocker-start'
```

Expected: both packages `ii`, every initrd present with a recent time and a size of
tens of megabytes, and the two `lsinitrd` lines present. If an initrd is missing,
empty or much smaller than its backup, or `apt` reported an error, **STOP**, restore
from the backups (section 12) and tell the person.

The agent does not do anything yet: it has no machine identity and no enrolled key
holder, so it does not start usefully until section 8 has run.

## 7. Get the phone ready

Ask the person to do this on their Android phone **before** you continue, and to
tell you when it is done:

1. Install an F-Droid client, and add the repository
   `https://muelli.github.io/synctang/fdroid/repo` (or scan the QR code on
   <https://muelli.github.io/synctang/>). Check the repository fingerprint shown
   against the one on that page.
2. Install **synctang key holder**, version **from 2026-10-28 or later**. Earlier
   versions have a bug on some phones (key use hangs on a Pixel 3a XL) that makes
   every unlock fail.
3. Open it. On some phones it shows a screen saying the phone cannot protect the key
   with its hardware. Explain that this means the key will live in software (so
   anything with root on the phone could copy it) and let the person decide; the
   button on that screen is their consent. It is a known limit of some older phones.
4. Be ready to grant the camera permission when asked.

## 8. Pair the phone (the person runs this)

This is the step with the passphrase and the QR code, so **the person runs it in
their own terminal**, not you: it needs the passphrase (rule 1), and the QR code
has to be on a screen they can scan (a terminal you drive may not show it to them
properly). Give them this block exactly, with `<DEV>` filled in with the device they
confirmed, and a name for the machine instead of `<name>` (the host name is a good
choice). Tell them to open a root shell first, because the passphrase is handed to
the command through a process substitution, which `sudo` would close:

```sh
sudo -i
cryptsetup open --test-passphrase --type luks2 <DEV> && echo "passphrase ok"
read -rsp 'LUKS passphrase: ' PASS; echo
unlocker pair --device <DEV> --name <name> --timeout 15m \
  --existing-passphrase-file <(printf %s "$PASS")
unset PASS
```

The `--test-passphrase` line asks for the passphrase, checks it against the header
without opening anything, and prints `passphrase ok`. It is there because
`unlocker pair` does **not** check the passphrase when it starts: a typo only shows
up at the end, after the phone has scanned and the person has confirmed. Have them
fix a typo here, then type it again at the `read` prompt.

What happens, so you can talk them through it:

1. The command prints a **QR code** and, under it, a **link** (`synctang://pair?...`). On a terminal that can show
   images (Kitty, Ghostty, iTerm2, WezTerm) it is drawn as a picture; elsewhere as
   blocks. `--qr blocks`, `--qr plain` and `--qr kitty` or `--qr iterm2` choose
   explicitly (`--qr plain` is the one for a screen that cannot show blocks).
2. In the app, on the pairing screen, they tap **Allow the camera**, grant it, and
   scan the code. If the camera cannot read it, they **paste the whole link** into
   the app's text field and press Pair. A bare device ID is not enough, because only
   the link carries the one-time code.
3. The machine prints `a key holder answered the pairing code`, with the key
   holder's **transport id** and the start of its **public key**, and asks
   `enrol it on this volume? [y/N]`. The person should answer `y` only if they just
   scanned that code themselves. (The app shows the same key's identity under
   About this phone.)
4. The command prints `paired. The key holder can now unlock this volume.` and the
   app shows `Paired with <name>`.

If nothing answers within the window, the command ends with `nobody paired` and they
run it again. The code lasts the `--timeout` and enrols one key holder.

Now check it from your own shell. This is read-only and needs no passphrase:

```sh
sudo unlocker status --device "$DEV"
```

Expected: this machine's transport id, `machine name shown to key holders: <name>`,
`enrolled key holders: 1`, and a line for the phone's transport id with its keyslot.
Write down the machine's **transport id** (a long dash-separated string); it is what
the console will print at boot.

## 9. Put the machine's identity into the initrd

Pairing created `/etc/unlocker/machine.pem`. The initrd must carry it, so rebuild
the initrds once more:

```sh
sudo dracut --force --regenerate-all
```

Verify, for the running kernel:

```sh
sudo lsinitrd /boot/initrd.img-"$(uname -r)" | grep -E 'usr/local/bin/unlocker|etc/unlocker/machine.pem|99-unlocker-start|95unlocker'
stat -c '%a %U %n' /boot/initrd.img-"$(uname -r)"
```

Expected: the agent binary, `etc/unlocker/machine.pem` (mode `-rw-------`), the start
hook and `95unlocker.conf` all listed, and the initrd mode `600 root`. A boot
partition that ignores file modes (FAT) will say so loudly in the package output;
tell the person if you saw that.

If the initrd lacks any of those, **STOP**: do not offer a reboot test.

## 10. Check that nothing was lost

```sh
sudo cryptsetup luksDump "$DEV" | sed -n '/^Keyslots:/,/^Digests:/p' | grep -E '^Keyslots:|^  [0-9]+: |^Tokens:'
sudo ls -l /root/synctang-backup
```

Expected: every keyslot from section 3 is still there, plus one new one, and a new
`mr-1` token. Optionally the person can confirm their passphrase still works, in
their own terminal, without opening anything:
`sudo cryptsetup open --test-passphrase "$DEV"`.

Summarise for the person what you did, the backup location, the machine's
transport id, and that the passphrase still unlocks the disk. Then ask whether they
want the reboot test.

## 11. Optional: test the reboot

Only if the person said yes **and** has console access (section 2). This is the one
step that proves the whole thing, and the only one that can go wrong in a way that
needs the console. Tell them first, in these words or close to them:

- The machine will stop at its LUKS passphrase prompt. That is expected; it is
  waiting for the phone.
- The console should print `unlocker: waiting for a key holder, transport id <the id
  you wrote down>`. Wait about twenty seconds after that for the machine to reach the
  relay.
- On the phone: **Unlock**, then **Approve**, then the fingerprint, within about a
  minute and a half (the app gives up on an unanswered Approve after 90 seconds, and
  the machine ends the session soon after). The phone needs Internet access, or to be
  on the same network as the machine.
- **If the phone does not work, type the passphrase at the console.** That is the
  fallback and it is unchanged.
- Your session will end when the machine reboots. They should come back to you after
  it is up and say how it went.

Ask once more: **"Reboot now?"** Run `sudo systemctl reboot` only on a clear yes.

When they come back, check from the host:

```sh
sudo journalctl -b 0 --no-pager | grep -iE 'unlocker|recovery attempt' | head
pgrep -a unlocker || echo "no unlocker process, as expected after boot"
systemctl --failed --no-legend
```

If the phone unlocked it, the journal has `unlocker: password agent started` and
`recovery attempt succeeded`. If they typed the passphrase instead, those success
lines are absent: **say so honestly** and go to section 13. Either way, record that
the boot completed.

## 12. Roll back

To undo the initrd changes (the backups are in `/root/synctang-backup`):

```sh
sudo cp -a /root/synctang-backup/initrd.img-<kernel version> /boot/
```

for each kernel (`ls /root/synctang-backup`). To remove the software:

```sh
sudo apt remove synctang-dracut synctang-unlocker
sudo dracut --force --regenerate-all
```

That rebuilds the initrds without the agent. It leaves the extra LUKS keyslot in
place, which is harmless but still opens the volume for that key holder. **Revoking
a key holder is a deliberate act for the person to ask for**, naming the key holder
(the transport id from `unlocker status`):

```sh
sudo -i
read -rsp 'LUKS passphrase: ' PASS; echo
unlocker enrol --device <DEV> --remove <transport id> --existing-passphrase-file <(printf %s "$PASS")
unset PASS
```

If the machine does not boot and the console shows a dracut emergency shell, or
`rd.unlocker=0` is needed to get past a stuck agent: at the bootloader, edit the
kernel line for one boot and add `rd.unlocker=0`; the agent is then skipped and the
passphrase prompt works as usual. Then restore the initrds as above.

## 13. Troubleshooting

| Symptom | Likely cause and what to do |
|---|---|
| The phone says the machine could not enrol it, and the command prints `enrolling the paired key holder: ... (still waiting)` | Almost always a mistyped passphrase (hence the check in section 8). Nothing was written to the header. Stop the command with Ctrl-C and run the block in section 8 again, typing the passphrase carefully. |
| `nobody paired within 15m0s` | The app was not opened or the camera was refused. Run the block in section 8 again. |
| The app says it could not reach the machine while pairing | The phone needs Internet access (the public relay) or to be on the machine's network. Check both are online. |
| After a reboot nothing happens at the prompt, and the console shows no `unlocker:` line | The agent is not in the initrd or was disabled. After typing the passphrase, check `lsinitrd` (section 9) and the kernel command line for `rd.unlocker=0`. |
| The console shows `waiting for a key holder` but the phone cannot connect | The machine has no network in the initrd. It needs DHCP on a wired interface; static addressing and Wi-Fi in the initrd are not covered. After a boot, look at `journalctl -b 0 | grep -iE 'networkd|dhcp'` and `ip -br addr`. |
| The app says "The fingerprint or face check ran out" | An old app (before 2026-10-28) on a phone whose key is in software. Update the app and pair again. |
| "Not recognized", then the sensor locks for 30 seconds | The fingerprint sensor, not synctang. Wait and try again, or type the passphrase. |
| `unlocker status` says `Failed to get token 0 for export` | It ran while another command was writing the header. Run it again. |
| The Approve screen says the machine stopped waiting | It sat for over 90 seconds. Tap Try again and Unlock. |

The agent's own log goes to the systemd journal (`journalctl -b 0 | grep unlocker`),
and only warnings and errors are repeated on the console. The agent takes
`--log-level trace` for more detail, but the initrd hook starts it without that
option, so for a trace you would need to start it by hand on a running system.

## 14. Report to the person

Say plainly what was done and what was not: the packages and versions installed, the
device, the backup location and that it should be copied off the machine, the
machine's transport id, the phone that was paired, whether the reboot test was run
and whether it was the phone or the passphrase that unlocked the machine, anything
you stopped on, and how to roll back. Do not claim the reboot test passed if it was
not run.

## Appendix: a laptop as the key holder

Use this instead of, or as well as, the phone. On the laptop, with the
`synctang-keyholder` package installed (`sudo apt install ./synctang-keyholder_*.deb`
from the same artifact):

```sh
keyholder init
keyholder export-pubkey
```

That prints a public key (`S:`) and a `transport-id:`. On the machine, as root and
with the passphrase typed by the person (rule 1), enrol it by hand:

```sh
sudo -i
read -rsp 'LUKS passphrase: ' PASS; echo
unlocker enrol --device <DEV> --name <name> --pubkey <S> --recipient-transport-id <transport-id> \
  --existing-passphrase-file <(printf %s "$PASS")
unset PASS
exit
sudo dracut --force --regenerate-all
```

It prints the machine's transport id. To unlock at boot, on the laptop:

```sh
keyholder unlock <the machine's transport id>
```

It shows the machine's name and waits for Enter before it answers.
