<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Threat model

**Disk thief, or anyone with an image of the disk.** Whoever has the
disk, an image of it, or a backup of `/boot` and the LUKS header has the
LUKS2 token and the machine's transport identity. Both live on the
unencrypted disk by design (the header is useless without a key holder,
per MR-1's properties in [mr1-protocol.md](mr1-protocol.md)). But that
pair is enough to *be* the machine as far as a key holder can tell, and
that is stronger than being able to ask:

- Such a party can present the machine's Device ID to your key holder,
  send any challenge it likes (including the unblinded one, which skips
  the blinding entirely), and recover the volume secret from the
  answer exactly as the real machine would `[I]` (an independent
  reviewer ran the algebra, 2026-10-23). Blinding protects against a
  key holder and an eavesdropper, not against a machine impersonator.
  **One approval from you is therefore enough to hand over the volume
  secret**, and the keyslot in the copied image keeps opening with it
  even if you revoke the key holder later.
- The approval need not be careless. If the copy is running while you
  reboot the real machine, both announce the same Device ID, your key
  holder takes the first connection that completes the handshake, and
  you can approve the wrong one `[I]`. Approve only when you yourself are
  rebooting that machine and you can see its console.
- After any suspected exposure of the disk or a backup, treat the volume
  secret as compromised: change the LUKS passphrase, revoke every key
  holder and enrol them again. If the exposure included an approval or a
  keyslot passphrase (see "An approval is more than an unlock" below), that
  is not enough: the volume key may have been read, and only encrypting
  the volume again under a new one (`cryptsetup reencrypt`) ends access.
- Sealing the machine's transport identity in the TPM (a PCR policy, so
  the disk alone cannot impersonate the machine) is the change that would
  remove this. It is real additional work and is not built. `[I]` not
  implemented.
- The machine's identity is also inside the initrd image under `/boot`, so
  anyone who can read that image can pose as the machine. dracut 110 (the
  version in Ubuntu 26.04) builds images under `umask 077` `[V]` (read in
  its source; the resulting file mode was not observed on a running
  system). As a safety net, installing `synctang-dracut` runs
  `secure-initrd.sh`, which restricts any image under `/boot` that holds the
  identity to root only and fails loudly where it cannot, because a FAT
  boot partition ignores `chmod` unless mounted with a restrictive umask
  `[V]` (`scripts/test-secure-initrd.sh`, against a temporary directory).

**An approval is more than an unlock.** The secret that a key holder's approval recovers is the passphrase
of that key holder's own keyslot: the agent hands exactly this to systemd at boot, and the machine holds it for
the moment of the unlock. Whoever gets hold of it can do what the passphrase of any keyslot can, and that is a lot:

- Unlock the volume with no key holder at all. `synctang-unlocker extract-keyslot-key` shows this: it waits for
  an approval and prints the passphrase `[V]` (run on a test volume, 2026-11-19). It is described only in the
  manual page, because it is not a step in setting anything up.
- Add a keyslot, whether for another key holder or for an ordinary passphrase, because `cryptsetup luksAddKey`
  takes the passphrase of any slot `[V]` (a passphrase added this way enrolled a second key holder in the test).
  `synctang-unlocker enrol` and `pair` can take an approval in place of the original passphrase for the first
  of these.
- Destroy keyslots: `cryptsetup luksKillSlot` takes the passphrase of any slot, including the slot of the
  owner's own passphrase and those of every other key holder, and it can destroy the slot it was given
  `[V]` (a review ran it on a scratch volume, 2026-11-19, and left no keyslot).
- Read out the volume key itself: `cryptsetup luksDump --dump-volume-key` with that passphrase prints it
  `[V]` (same review). The volume key is what the data is encrypted with. **No revoke and no removal of keyslots
  ends access for whoever has it**: with the volume key `cryptsetup` opens the volume, and adds a new passphrase,
  without a single keyslot left `[V]`. Only encrypting the volume again under a new volume key
  (`cryptsetup reencrypt`) ends it, after which the key holders have to be enrolled again.

So `revoke` (which destroys the keyslot of one key holder and nothing else) is enough only if the approval or
the passphrase was never used for anything but opening the volume. After an approval you doubt, treat the volume
key as read, and re-encrypt. A keyslot added with the passphrase is independent of the key holder, and so is a
copy of the passphrase. This is how LUKS is made, and not special to synctang: the passphrase of any LUKS keyslot
can do all of the above. What synctang adds is that a key holder's approval hands one over.

The machine's root, malware on the machine, and anyone who obtains one approval from your key holder for a
copy of the disk (see above) are the parties who can reach this; each needs an approval, and a key holder
shows who is asking, but it cannot show what the approval will be used for: it sees a request to unlock, and
the machine decides what to do with the answer. Whoever can run the stock programs as root on the machine can
ask for an approval under that description; a person who writes their own program can ask under any. Tang has
the same property: `clevis luks bind` adds a random-key keyslot and leaves the original passphrase in place,
and `clevis luks pass` prints the passphrase of a slot that was bound to a Tang server `[V]` (Clevis manual
pages).

What the key holder does to help, cheapest first:

- It shows the requester's Device ID and name before approving
  (`mrcore.Hello`), and answers at most once per session. The name is
  chosen by the machine and proves nothing; the Device ID is the verified
  one. `[V]` `synctang-keyholder unlock` waits for the human to press Enter (or
  pass `--yes`) before answering.
- There is no confirm code. An earlier version had the machine print a
  code on its console and send the same code to the key holder, to be
  handed back before recovery. Any key holder could hand it back without
  seeing the console, and a copy of the disk could simply not ask, so it
  protected nothing and was removed before any release `[I]`. The second
  factor that would help is a threshold (below).
- On Android the key is kept wrapped by a hardware key that needs a strong
  biometric for every single use, bound to that one operation through a
  `BiometricPrompt.CryptoObject`: the secure hardware refuses to unwrap
  unless the sensor has just matched for exactly that operation. There is no
  time window that any other fingerprint unlock of the phone could open. That a
  key that needs a fingerprint refuses to unwrap when none was presented is `[V]`
  (Pixel 3a XL, 2026-11-01). That a fingerprint given for one operation cannot be
  spent on another is `[V]` too (`OperationBindingTest`, Pixel 3a XL, 2026-11-02, a
  finger on the sensor): of two prepared operations on the same key, one was
  authenticated; the other was then refused with Keymaster's "key user not
  authenticated" (-26), the authenticated one unwrapped, and a second unwrapping
  with it was refused as well. That matches how the Keystore documents the setting
  and what the reference implementation does (`keymaster_enforcement.cpp` and
  keystore2's token receiver map in AOSP `android12-release`, read, not run); the
  Pixel's own Keymaster is closed code and was not examined, and which layer refused
  cannot be told from the outside. What this does not do is keep the scalar out of the app's memory
  while it is used: see the next section.

**Key holder compromise.** On the laptop the long-term key is kept by a store that is chosen
when it is made (`synctang-keyholder init --store`) and that `synctang-keyholder status` names.
What each protects, against four attackers:

| | plain file | keyring (`secret-service`) | `systemd-creds` | `systemd-creds:tpm2` (not run on a TPM, see below) |
| --- | --- | --- | --- | --- |
| A copy of `~/.config` (a backup, a synced folder) | no: the file is the key | yes, for GNOME Keyring with a keyring password (run): encrypted with a key your login unlocks | yes: sealed by systemd for your account on this machine | yes |
| An image of the whole disk of an unencrypted laptop | no | yes, until the login password is guessed | no: the key that unseals it is on the same disk | yes: it is sealed to the TPM |
| Another user of the laptop | yes | yes | yes | yes |
| Software running as you, after login | no | no: the keyring is unlocked for the session and answers any process of yours | no: it can run `systemd-creds` itself | no |
| Root | no | no | no | partly: it can use the TPM, not copy the key |

None of them stops software running as you: that needs something software cannot supply, a touch on a
hardware key, and nothing here uses one. The prompt that `synctang-keyholder unlock` shows is a
good habit and not a boundary, because the same software can call the same library. The
keyring row is GNOME Keyring, the only one supported and the one that was run. A keyring with a blank
password, which automatic login often gives, protects nothing against a copy of its files. The TPM variant was never run on
a machine with a TPM (the test machine has none), so what it protects is what systemd documents and not
something seen here; it asks `systemd-analyze has-tpm2` first and refuses where that fails, because
`systemd-creds` would otherwise seal to the machine key alone and say nothing. The TPM variant ties the key to one machine: it cannot be moved, and may be lost if the TPM or its measured
state changes, so the key needs a backup some other way. The keyring needs a desktop session and a D-Bus
session, which plain ssh does not give; `systemd-creds` needs systemd 256 or later but no desktop.
The key passes through a Go string inside the keyring library and cannot be wiped from memory
there, nor in the string the program makes to hand it over, where the file and credential stores keep their
copies in byte slices and wipe them. On Android, see the next section.

**A leaked pairing link.** Pairing (`synctang-unlocker pair`, then `synctang-keyholder pair` or the app)
turns a one-time code into an enrolled key holder. The code authenticates the key holder to the machine
and nothing more, so whoever uses it first, while it lasts (until one key holder is enrolled or the five minutes
are over; a wrong code does not use it up), is offered to the operator as a key holder, and if the operator says
yes they hold a keyslot that opens the disk. The link leaks by being photographed, read over a shoulder, or
pasted into a chat or a shell history. The defence is the operator's check: the machine and the real key
holder both show a six-digit code derived from both Device IDs, the key being enrolled and a nonce from each
side (`mrcore.PairSAS`), and the operator enrols only if the two match.

That holds because each side commits before it sees the other. The key holder's request carries the key to be
enrolled, a commitment to a nonce of its own, and a tag made with the pairing code; the machine sends its own
nonce only if the tag is good; and the key holder reveals its nonce afterwards. The code is made from both
nonces. Whoever plays the machine has to choose its nonce without knowing the key holder's, and whoever plays
the key holder has to choose its own before it has seen the machine's, so neither can fit the code: one try in
a million for each connection. Earlier versions did not hold. With the machine's nonce sent first, a key
holder could grind its key to any code it liked. With only the key holder committed, whoever played the machine
to a key holder could fit its nonce, so a link passed on with another machine's identity in it made both codes
agree and the real machine enrol the attacker's key. A review found both, and ran the second on 2026-11-19
(about a million tries, half a second). `[V]` A test runs that attack against the machine and the phone library,
and the codes now differ; `[V]` another in memory against the primitives. A QR code scanned off the machine's own
screen was never open to it, since the key holder then dials the real machine.

Each completed connection is one chance in a million for somebody who has the pairing code: they can take the
nonce, look at the code, and leave, up to three times, and then the window ends. The machine counts the
connections that were given a nonce and ended without a valid answer, or with the operator's no, and ends the
window after three, saying that the link has leaked
`[V]`. A peer that does not have the code gets no nonce, so no code to look at, and does not count. So over one
window the chance of a match for somebody with the link is a few in a million, and the code is checked by the
operator at that.

What the operator sees when somebody else answered first is not a refusal: the machine asks about a key holder
while the laptop or phone shows no code yet, or a code that is different. The laptop keeps dialling until its
`--timeout` and then says it could not reach the machine. This is only as good as the comparison: an operator
who answers yes without looking gets no protection from it, and `--yes` gives the check up on purpose. The key
holder's name that the machine shows is cleaned of control characters, and the key id shown is a hash of the
public key.

Anyone who can reach the machine can open a pairing connection and so hold the pairing slot while the operator
is asked about it: that blocks pairing for the length of the window, and does nothing else. The question has no
time limit of its own and stays open after the window ends.

`[V]` The two programs show the same code in a test that runs both (`integration/`). The phone app shows the same
code on its pairing screen while the operator decides (`[V]` on an emulator: the screen draws it, it is read out
digit by digit, and it adds nothing to focus; `[V]` in a test that the code the library offers is what the
machine works out; `[U]` not tried against a real machine from the app itself, since the pairing flow there is
a scan on a phone). The machine's side of the link, its transport id, is checked by the key holder against the
certificate it connects to. That stops a different machine from answering the link it was given; it does not
stop a link that names a different machine, which the key holder pairs with as asked. That is what the
comparison of the codes catches (above). A link is also somebody's word about names: `synctang-keyholder pair` does not let the name in it take over a name the
person already uses for another machine unless they pass `--force`, refuses a link with a setting it does not
know or one given twice, and shows the name in it with control characters made harmless; the phone cleans and
cuts the name in a link the same way and, when another paired machine has the same name, keeps the new one
apart by adding the first group of its Device ID.

**The phone's key is wrapped, not held, and what that means.** The MR-1 key
agreement could be a Keystore key (`PURPOSE_AGREE_KEY`), non-exportable and
used inside the secure hardware. It is not, because that only works in
hardware on a phone with a KeyMint implementation. On a Pixel 3a XL (Android
12) such a key is created at security level **software** whether or not
StrongBox is requested, and its attestation says so `[V]` (2026-10-28). The
phone has a security chip (the Titan M services run and a StrongBox Keymaster
4.1 is declared, next to a Keymaster 4.0 in the Qualcomm TEE `[V]`, 2026-11-01),
but the Keymaster interface has no key agreement: Android 12's keystore compat
layer says "Since KeyMaster doesn't support ECDH, route all key creation
requests to soft-KeyMint if and only an ECDH key is requested"
(`km_compat.cpp`, `android12-release`) `[V]`. A key in software is no better
than a file, and using one hung for as long as anyone waited whenever StrongBox
had been requested `[V]`.

So the app makes the private scalar itself (in Go) and keeps it wrapped:
encrypted with RSA-OAEP under a public key whose private half is a Keystore key
in the phone's secure hardware, created so that each use needs a fresh strong
biometric. The wrapped scalar and the public point are kept in a directory
Android does not back up.

- **Protected:** the scalar at rest. A copy of the app's files, a backup or a
  disk image holds only the wrapped scalar, and the wrapping key cannot leave the
  hardware, so another phone cannot unwrap it either `[I]` (non-exportability is the
  Keystore's contract; no test tries to read the key out). Each unlock needs a
  fingerprint for that operation. The scalar is wrapped inside the app's own
  process, under a plain RSA key rebuilt from the certificate's, so that the
  Keystore daemon is never sent it for that `[V]` (on the Pixel the wrapping cipher
  runs in the `AndroidOpenSSL` provider, 2026-11-02).
- **Not protected:** the scalar while it is in use, and for some time after. During
  one answer it is in the app's memory and then in Go's. The copies the code holds
  are wiped afterwards (a new key is made by one Go call, so its scalar crosses
  into Java once, and Go's own copy is cleared with it; checking a scalar is in
  range allocates nothing, `[V]` by a test that counts allocations), but not every
  copy is the code's: the Keystore's reply passes through a native buffer, and the
  Java and Go runtimes copy slices across their boundary (gomobile's C copy of a
  returned slice is freed, not zeroed, and ART copies an array handed to native
  code). The
  P-256 library's own copy of the scalar is on the stack, not the heap, and is not
  zeroed either, only overwritten by later calls on that goroutine `[V]` (the
  compiler's escape analysis, `filippo.io/nistec` v0.0.4, amd64 and arm64). A
  process with root access to the phone while the app is alive can therefore find a
  scalar in freed memory after an unlock, not only during it `[I]` (read from the
  source of gomobile; memory was not examined). A
  non-exportable hardware key agreement key would not have this weakness; this
  phone cannot offer one. Whether a phone with KeyMint keeps such a key in
  hardware, and whether the app should then use it, is `[U]`: none was tried.
- **On the Pixel 3a XL:** the StrongBox chip creates a key and reports it at the
  StrongBox level, and then every use of it fails (`BACKEND_BUSY` from the key blob
  upgrade, for as long as anyone waits) `[V]`. So the app uses each candidate key
  once, with a time limit, before trusting it, and falls back from StrongBox to the
  TEE. The wrapping key there is in the TEE (security level 1, inside secure
  hardware) `[V]`. The phone's security patch level is 2022-05-05: it no longer
  receives updates.
- **A Keystore that does not answer:** starting the operation, before the
  fingerprint prompt, and the unwrapping afterwards are each limited to 20 seconds.
  Past that the app stops waiting, tells the machine no and says so (a stuck
  Keystore call cannot be interrupted, only abandoned; a late result is wiped when
  it arrives) `[V]` (unit tests with a Keystore that never answers; the real
  Keystore was never made to hang on the TEE key). A StrongBox attempt that is
  given up on is stopped by deleting its alias: the stuck call retries for a key id
  that no longer exists and fails with "not found" (read from the keystore2 source,
  `[I]`), which the platform reports as `KeyPermanentlyInvalidatedException`. On the
  Pixel that is what was seen: in 20 of 20 key creations the abandoned StrongBox
  trial ended with that exception 25 to 110 ms after the 8 second give-up and the
  alias deletion `[V]` (logcat, 2026-11-02).
- **A key that stopped working:** if the hardware refuses a fingerprint the prompt
  has just accepted, the machine is told no and the key is kept, because that is
  either an invalidation or a platform fault and the app cannot tell which; the next
  attempt starts an operation before the prompt, which says so for certain. Whether
  a key was invalidated while the app was closed, or while it was in the background
  (a finger enrolled in Settings), is checked at start and each time the app comes
  back to the foreground, behind the screen that is showing, so a dead public point
  is not offered for enrolment (not on every return to the home screen, because that
  check is a Keystore call and the one that has hung). It is skipped while a request
  is waiting. Enrolling a new kind of biometric (a face on a phone that had only
  fingerprints) invalidates the key as well. Both are `[V]` by unit tests with a fake Keystore; the refusal's code
  is read from the real exception on the Pixel (-26, "key user not authenticated").
- **A key judged unfinished is made again:** a wrapped key with no public file is
  taken for a creation that stopped (no public point was ever shown), so someone who
  can delete one file of a live key (root, or `run-as` on a debug build) turns it into
  a silent re-creation. That needs the access that makes the key readable anyway.
- **A key that is not whole:** the Keystore reports almost every error as "no such
  key", so a key found with one half missing may only have failed to answer. The
  app does not delete it. Only nothing at all, or a creation that stopped before
  the public point existed, is made afresh; anything else stops at a screen that
  offers trying again or making a new key (which throws the old one away, and has
  no keyboard shortcut). The same screen appears if the saved scalar does not
  belong to the saved public point, which the Go side checks before answering.
  `[V]` by unit tests of the decision and of the screen, and on the Pixel by
  instrumented tests that delete one half and check the other is kept and
  `ensureKey` refuses (2026-11-02). The screen itself was not shown on the phone
  `[U]`.
- **Where the hardware half ends up in software** (an emulator, a phone with no
  secure hardware for it), the wrapping protects nothing the file would not, and
  the app refuses to go on unless the owner presses "Use a software key on this
  phone" (a choice kept for that phone).

An earlier version relied on the key-agreement key and a five-second time window
after any fingerprint match. That is gone, and so is any compatibility with the
old key: a phone that had one has to be paired again.

**Rendezvous infrastructure.** The Syncthing relay network sees two
Device IDs and traffic volume, nothing else: the tunnel itself is TLS
1.3 with certificate pinning by Device ID (`mrcore/transport`), so a
relay operator cannot read or inject into an unlock exchange, only
observe that one happened between two identities and roughly how much
data moved. Availability depends on the public relay pool; a
project-run rendezvous server (transport implementation T2 in the
design plan) would remove that dependency, and is not built. Running
your own relay and discovery server is possible and moves that dependency
to the owner, without changing what is seen (`private-servers.md`).
`[V]` `transport.LocalDiscovery` is a partial answer already built: on
the same LAN, unlock needs no rendezvous infrastructure, no Internet
route and no DNS at all. Its multicast announcement is unauthenticated
(anyone on the LAN can see that a Device ID is listening, and where),
exactly as global discovery's lookups already are; nothing in it is
trusted on its own; the TLS handshake and Device ID pinning that follow
a dial are unchanged, so a forged announcement can point a dialer at
the wrong address but cannot make it accept the wrong identity. It can
make a dialler wait: every TLS handshake is bounded to ten seconds, so a
peer that connects and says nothing holds a listener, or a dial, for that
long and no longer `[V]` (`TestServerHandshakeGivesUpOnASilentPeer`).

**Threshold.** Not built, and parked for a later round. Requiring the
answers of two key holders (say, the phone and the laptop) is the
second factor that would remove "one approval is enough". Recipients
already share one LUKS2 token, but each has its own `P` and keyslot
today (`docs/mr1-spec.md`, section 14), so a threshold needs one `P`
split into shares, one per recipient, a token format that records how
many are needed, and an agent that collects that many answers in one
boot `[I]`. The key holders' side of the exchange would not change.

**What this project does not defend against**: an attacker who controls
both the disk and a key holder's approval, which includes a human who is
coerced, careless, or simply approves the attacker's copy of the machine
by mistake (see the disk-thief section above: one approval is enough);
compromise of the Syncthing relay operator's
infrastructure at a scale that lets them run their own TLS endpoint
convincingly (certificate pinning by Device ID is what stops an ordinary
man-in-the-middle, not a compromised CA, since there is no CA in this
design at all: identities are self-signed and pinned by hash); and,
deliberately, unattended reboots, which is the whole trade this design
makes.
