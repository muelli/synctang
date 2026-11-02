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
  holder and enrol them again.
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

What the key holder does to help, cheapest first:

- It shows the requester's Device ID and name before approving
  (`mrcore.Hello`), and answers at most once per session. The name is
  chosen by the machine and proves nothing; the Device ID is the verified
  one. `[V]` `keyholder unlock` waits for the human to press Enter (or
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
  spent on another is `[I]`: it is how the Keystore documents the setting, and what
  the reference implementation does (`keymaster_enforcement.cpp` and keystore2's
  token receiver map in AOSP `android12-release`, read, not run); the Pixel's own
  Keymaster is closed code and was not examined. A test that can show it exists
  (`OperationBindingTest`: authenticate one of two prepared operations, expect the
  other to be refused and the first to work exactly once) and needs a person at the
  sensor; it has not been run `[U]`. What this does not do is keep the scalar out of the app's memory
  while it is used: see the next section.

**Key holder compromise.** On the laptop, a plain key file is the weak
point; a TPM2 ECDH backend (behind a build tag) is the intended fix.
`[U]` whether `go-tpm` integration is reachable in the time available;
may remain a stub with its tests skipped, in which case the file backend
is what actually protects a laptop key holder, no better than any other
secret in a home directory. On Android, see the next section.

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
  are wiped afterwards, but not every copy is the code's: the Keystore's reply
  passes through a native buffer, and the Java and Go runtimes copy slices across
  their boundary (gomobile's C copy of a returned slice is freed, not zeroed). The
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
  a key was invalidated while the app was closed is checked once at start, in the
  background, so a dead public point is not offered for enrolment (not on every
  return to the home screen, because that check is a Keystore call and the one that
  has hung). Both are `[V]` by unit tests with a fake Keystore; the refusal's code
  is read from the real exception on the Pixel (-26, "key user not authenticated").
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
design plan) would remove that dependency, and is not built.
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
