<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# The MR-1 protocol

MR-1 is a McCallum-Relyea exchange (the same blinding construction Tang
uses), adapted so the key holder's long-term secret can live in hardware
that only offers ECDH, not full scalar multiplication (Android Keystore,
TPM2). `[V]` implemented in `mrcore`, see `mrcore/mr1.go` and the tests
listed below.

Curve: NIST P-256. Chosen over Curve25519 because Android Keystore and
TPM2 support ECDH on P-256, not on Curve25519.

A **key holder** has a long-term private scalar `s` and publishes the
point `S = s.G` and `kid = SHA-256(S)`.

**Enrol** (machine, once per key holder):

1. Generate a random 32-byte volume secret `P`; hex-encode it and add
   *that* as a new LUKS2 keyslot with `cryptsetup luksAddKey` (the
   existing passphrase is required), never the raw bytes: systemd's
   ask-password protocol (used at recovery time, below) silently
   truncates a raw binary answer at its first embedded NUL byte, which
   a uniformly random 32-byte secret contains roughly one enrolment in
   eight. Found running against a real deployment; see `STATUS.md`.
   `[V]` `mrcore.Enrol`, `cmd/unlocker`'s `enrolRecipient` and
   `luksKeyMaterial`.
2. Pick a random scalar `c`, compute `C = c.G` and `K = c.S`. Derive
   `k = HKDF-SHA256(x(K), salt = kid, info = "mr-1 enrol")`.
3. `ct = AES-256-GCM(k, nonce, P)`.
4. Store a LUKS2 token of type `mr-1` carrying, per recipient: `kid`, `S`,
   `C`, `ct`, `nonce`, and an opaque `transport` blob (that recipient's
   transport identity). Several recipients can share one token; any one
   of them can recover `P`. Discard `c`, `K`, `k`, `P` from memory once
   the keyslot and token are written. Nothing on disk is secret on its
   own, matching Tang's property that the header is useless without the
   key holder.

**Recover** (machine, per attempt):

1. Pick an ephemeral scalar `e`, compute `E = e.G`, and send
   `X = C + E` together with `kid` to the key holder
   (`mrcore.RecoverRequest`).
2. The key holder answers in one of two ways, depending on what its key
   storage can do:
   - A key holder that holds `s` directly (the `keyholder` file backend,
     or Tang's server) computes the full point `Y = s.X` and returns it.
     `[V]` `mrcore.FinishFullPoint`.
   - A key holder whose key lives in hardware that only offers ECDH
     (Android Keystore, TPM2) can only return the raw x-coordinate
     `x(s.X)`, 32 bytes, never the full point. `[V]` on a real
     StrongBox phone (`TESTREPORT.md` A4); the earlier emulator spike is
     in `docs/wp0-keystore-ecdh.md`.
3. Either way, the machine recovers the shared point: given the full
   point directly, or given only `x(s.X)`, by lifting `x` to its two
   candidate points `+/-Y` and trying both. It computes
   `K' = Y - e.S`, derives `k'` exactly as enrolment derived `k`, and
   attempts to open the AEAD with each candidate; the authentication tag
   picks the right one, since `K' = s.C = c.S = K` only for the true `Y`.
   `[V]` `mrcore.FinishFullPoint` / `mrcore.FinishXOnly`, see
   `mrcore/mr1_test.go`'s `TestEnrolRecoverXOnlyRoundTrip` and
   `TestRecoverWrongKeyFails` (a wrong key holder, or the wrong sign
   candidate, fails the AEAD tag with an error, never a panic).
4. Decrypt `P`, hex-encode it (the same encoding enrolment added to the
   keyslot, for the same reason), verify that the encoded form actually
   opens the volume (`cryptsetup open --test-passphrase`), answer
   systemd's password agent request with it, and wipe `e`, `P` and the
   encoded form from memory.

Properties, compared with Tang: the key holder never learns `P` or `K`
(identical blinding); a passive or active network attacker learns
nothing usable; a fake key holder yields a wrong key, caught by the AEAD
tag; and, unlike Tang, the key holder's secret can be non-exportable and
gated per use (StrongBox plus biometric on Android). The trade-off is a
human in the loop for every unlock: better against theft, worse for
unattended reboots. Unattended boot is deliberately out of scope; see the README.

**Test vectors**: `testdata/mr1.json` is a full worked example (a fixed
recipient keypair, a fixed enrolment, and a fixed recovery, both the
full-point and x-only paths) with every value hex-encoded, generated
once by `mrcore`'s own tested implementation and self-checked before
being committed. `[V]` consumed by `mrcore/vectors_test.go`; intended for
WP5's Android instrumented tests to verify an independent Kotlin
implementation against the same numbers, not just against `mrcore`'s own
tests.

**Wire format**: the recovery exchange is two JSON messages over the
transport's authenticated connection, each prefixed with a 4-byte
big-endian length (`mrcore.WriteMessage`/`ReadMessage`,
`mrcore.RecoverRequest`/`RecoverResponse`). A `Hello` message (the
machine's name) is sent first, and, when `--confirm-code` is active, a
`ConfirmCodeChallenge`/`ConfirmCodeResponse` pair is exchanged before the
recovery request; see [threat-model.md](threat-model.md) for why.

## Pairing

`unlocker pair` shows a QR code and a one-time code, then enrols the key
holder that answers it. The QR payload is
`synctang://pair?machine=<device id>&name=<name>&psk=<base32 secret>`.

The QR code is deliberately not the whole story, and it is worth being
clear about why, because the comparable design in `syncthing-socket`
does fit in a single scan. Its QR code carries the volume's passphrase,
which is what lets the phone finish enrolment on its own, and its setup
script warns you not to photograph the code for exactly that reason.
MR-1 never puts the passphrase on the phone in any form, so what has to
travel is this key holder's *public key*, and it has to travel towards
the machine, which has a screen but no camera. Hence a short
conversation over the network rather than a one-way transfer.

Everything the code is good for is bounded: it lasts five minutes by
default (`--timeout`), it enrols one key holder, it is proved over a
TLS channel whose far end the phone has already pinned to the Device ID
from the same QR code, and the proof is bound to both identities and to
a fresh nonce, so it is worth nothing replayed or on a second phone.
Anybody who photographs the code can still *ask*, which is why you are
shown who is asking and have to agree (`--yes` skips the question, for
automation).
