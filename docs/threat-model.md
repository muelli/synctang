<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Threat model

**Disk thief during an unlock window.** Whoever steals the machine has
the LUKS2 token and the machine's transport identity: both live on the
unencrypted disk by design (the header is useless without a key holder,
per MR-1's properties in [mr1-protocol.md](mr1-protocol.md), but the thief can still *ask*). Mitigations,
cheapest first:

- The key holder shows the requester's transport ID and name before
  approving (`mrcore.Hello`), and answers at most once per session.
  `[V]` `keyholder unlock` waits for the human to press Enter (or pass
  `--yes`) before answering.
- **Confirm-code mode** (`unlocker agent --confirm-code`): the machine
  prints a six-digit code to its own console; the key holder must relay
  it back before the machine will even send a recovery request. A thief
  who only has the disk, not eyes on the machine's console, cannot
  complete a recovery over the network alone. `[V]`
  `mrcore.ConfirmCodeChallenge`/`ConfirmCodeResponse`.
- Sealing the machine's transport identity in the TPM (a PCR policy, so
  the disk alone cannot impersonate the machine) is real additional work,
  deferred behind a flag for a later version. `[I]` not implemented.

**Key holder compromise.** On the laptop, a plain key file is the weak
point; a TPM2 ECDH backend (behind a build tag) is the intended fix.
`[U]` whether `go-tpm` integration is reachable in the time available;
may remain a stub with its tests skipped, in which case the file backend
is what actually protects a laptop key holder, no better than any other
secret in a home directory. On Android, the key is intended to live in
StrongBox, biometric-gated per use. `[V]` on real StrongBox hardware:
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
the wrong address but cannot make it accept the wrong identity.

**Threshold.** Not built. Recipients are already first-class (several
key holders can share one LUKS2 token), so Shamir-sharing `P` across
them, one share per recipient, is straightforward future work rather
than a redesign.

**What this project does not defend against**: an attacker who controls
both the disk and a key holder's approval (a coerced or careless human
pressing Enter); compromise of the Syncthing relay operator's
infrastructure at a scale that lets them run their own TLS endpoint
convincingly (certificate pinning by Device ID is what stops an ordinary
man-in-the-middle, not a compromised CA, since there is no CA in this
design at all: identities are self-signed and pinned by hash); and,
deliberately, unattended reboots, which is the whole trade this design
makes.
