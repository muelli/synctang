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
- The machine's identity is also inside the initrd image under `/boot`.
  Whether that file is readable by other local users depends on how
  dracut creates it on your distribution; check `ls -l /boot/initrd*`
  and keep it `0600`. `[U]` not checked on any target system.

What the key holder does to help, cheapest first:

- It shows the requester's Device ID and name before approving
  (`mrcore.Hello`), and answers at most once per session. The name is
  chosen by the machine and proves nothing; the Device ID is the verified
  one. `[V]` `keyholder unlock` waits for the human to press Enter (or
  pass `--yes`) before answering.
- **Confirm-code mode** (`unlocker agent --confirm-code`, or
  `rd.unlocker.confirm_code=1`): the machine prints a six-digit code on
  its own console and the key holder must relay it back before the
  machine sends its recovery request. This shows the person standing at
  the console that the request comes from the machine in front of them,
  which exposes an impersonator whose console they cannot see. **It does
  not stop someone holding a copy of the disk.** The machine decides
  whether to ask, an empty code means "not required", and both key
  holders skip the step when it is empty, so an impersonator just does not
  ask `[I]`. The laptop key holder makes you type the code; the phone
  app currently shows the code it received and sends it back on one tap,
  so on the phone it checks nothing `[I]`. There is also no limit on
  wrong attempts (about 11 to 12 days to guess six digits at one every two
  seconds). `[V]` the message exchange itself,
  `mrcore.ConfirmCodeChallenge`/`ConfirmCodeResponse`.
- On Android the key is created so the Keystore refuses to use it unless
  a strong biometric matched within the last 15 seconds. That is a time
  window, not a binding to the request you saw: code running inside the
  app, on a phone where it can run, could use the key during the window
  after any fingerprint unlock of the phone `[I]` (platform behaviour read
  from documentation, not tested).

**Key holder compromise.** On the laptop, a plain key file is the weak
point; a TPM2 ECDH backend (behind a build tag) is the intended fix.
`[U]` whether `go-tpm` integration is reachable in the time available;
may remain a stub with its tests skipped, in which case the file backend
is what actually protects a laptop key holder, no better than any other
secret in a home directory. On Android, the key is intended to live in
StrongBox, gated by a strong biometric within a short time window (see
above). `[V]` on real StrongBox hardware:
Android Keystore accepted a manually constructed peer point and returned
the correct raw x-coordinate, proved by the machine opening its volume
with it (`TESTREPORT.md` A4, 2026-09-21). The earlier emulator spike is
in `docs/wp0-keystore-ecdh.md`.

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

**Threshold.** Not built. Recipients are already first-class (several
key holders can share one LUKS2 token), so Shamir-sharing `P` across
them, one share per recipient, is straightforward future work rather
than a redesign.

**What this project does not defend against**: an attacker who controls
both the disk and a key holder's approval, which includes a human who is
coerced, careless, or simply approves the attacker's copy of the machine
by mistake (see the disk-thief section above: one approval is enough, and
the confirm code does not change that); compromise of the Syncthing relay operator's
infrastructure at a scale that lets them run their own TLS endpoint
convincingly (certificate pinning by Device ID is what stops an ordinary
man-in-the-middle, not a compromised CA, since there is no CA in this
design at all: identities are self-signed and pinned by hash); and,
deliberately, unattended reboots, which is the whole trade this design
makes.
