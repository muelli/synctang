<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# MR-1: a key-recovery protocol for non-exportable keys

Version 1 of the token format, draft specification.

This document is meant to be read by someone who has never seen the
project it came from. It defines MR-1 completely enough to write an
interoperating implementation from the text alone, and it was checked
that way (see section 11). Where it describes how the reference
implementation (the Go package `mrcore` in this repository) behaves, and
that behaviour is not something the protocol requires, it says so.

Markers: `[V]` verified by running something named in the text, `[I]`
intended or inferred from reading code or documents without running it,
`[U]` unknown or not checked. Requirement words (MUST, SHOULD, MAY) are
used in the sense of RFC 2119.

Contents:

1. Purpose and scope
2. Notation
3. The group
4. Encodings
5. Key derivation and encryption
6. The token
7. Enrolment
8. Recovery
9. Wire messages
10. Test vectors and a worked example
11. Validation of this specification
12. Security considerations
13. Differences from Tang
14. Disagreements between this specification and `docs/mr1-protocol.md`

## 1. Purpose and scope

MR-1 lets a **machine** protect a secret `P` (in practice the key of a
disk volume) so that it can be recovered only with the help of a **key
holder** that owns a long-term private scalar `s`, without the key
holder ever seeing `P`, and without the machine ever learning `s`. It is
a McCallum-Relyea exchange, the blinding construction used by Tang and
Clevis, adapted so that the key holder may be a device that can perform
only ECDH, not general scalar multiplication, and can therefore return
only the x-coordinate of a result (Android Keystore, TPM 2.0).

In scope: the group, the encodings, the key derivation, the authenticated
encryption, the on-disk token, enrolment, recovery, and the six
messages of the recovery exchange. Out of scope: how machine and key
holder find each other and authenticate each other (the transport), how
a key holder's public key reaches the machine (pairing), and what the
application does with the recovered `P`. Section 4.4 describes one
application profile (LUKS2) because the reference implementation has
one.

There are two roles. The **key holder** has a long-term scalar `s` and
publishes `S = s.G`. The **machine** stores a token and, at recovery
time, listens for the key holder to connect and then asks it for help.
In the reference implementation the machine listens and the key holder
dials `[I]` (`cmd/unlocker/agent.go`, `cmd/keyholder/unlock.go`); MR-1
itself does not depend on which side dials. Both
roles MUST speak over a channel that authenticates the peer's identity
and protects confidentiality and integrity; MR-1 does not provide that
itself (section 12.1).

## 2. Notation

- `G` is the group generator, `n` the group order. `+` and `-` are the
  group operation and its inverse on curve points. `a.P` is the scalar
  multiple of point `P` by integer `a`. `O` is the point at infinity.
- `x(P)` is the x-coordinate of point `P` as 32 bytes, big-endian.
  `x(O)` is undefined.
- `||` is byte-string concatenation. `len(b)` is the length of `b` in
  bytes.
- `SHA-256(b)` is the 32-byte digest. `HKDF(ikm, salt, info, L)` is
  HKDF (RFC 5869) with HMAC-SHA-256, both extract and expand, producing
  `L` bytes.
- `AEAD-Seal(k, nonce, m)` and `AEAD-Open(k, nonce, ct)` are AES-256-GCM
  with an empty associated-data string; see section 5.2.
- `hex(b)` is lowercase hexadecimal, two characters per byte.
  `b64(b)` is standard base64 (RFC 4648 section 4, alphabet `A-Z a-z 0-9
  + /`) with `=` padding.
- Names: `s` key holder's long-term scalar; `S = s.G` its public point;
  `kid = SHA-256(S)`; `c` the enrolment ephemeral scalar, `C = c.G`;
  `K = c.S` the enrolment shared point; `k` the AEAD key; `e` the
  recovery ephemeral scalar, `E = e.G`; `X = C + E` the challenge;
  `Y = s.X` the key holder's answer.

## 3. The group

MR-1 version 1 uses the NIST curve P-256 (also called secp256r1 and
prime256v1; FIPS 186-4, SEC 2). Its cofactor is 1.

- `p = ffffffff00000001000000000000000000000000ffffffffffffffffffffffff`
- `n = ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551`
- `G.x = 6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296`
- `G.y = 4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5`

Curve equation `y^2 = x^3 - 3x + b` over the field of `p` elements, with
`b = 5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b`.

P-256 was chosen over Curve25519 because Android Keystore and TPM 2.0
offer ECDH on P-256 and not on Curve25519 `[I]` (the project's recorded
reason, not independently checked here). That the x-only path works
against a StrongBox-held key on a real phone is recorded as verified in
the project's `TESTREPORT.md`, item A4; this document did not repeat it.

## 4. Encodings

### 4.1 Points

A point is encoded as SEC1 uncompressed: 65 bytes, `0x04 || X || Y`,
with `X` and `Y` each 32 bytes big-endian and each less than `p`.

Implementations MUST encode every point they emit (`S`, `C`, `X`, `Y`)
this way. Implementations MUST reject, on receipt, a point that is not
on the curve. The point at infinity is encoded as the single byte `0x00`;
it is never a legitimate value of `S`, `C`, `X` or `Y` and MUST be
refused where it arrives from a peer (section 8.2).

Behaviour of the reference implementation beyond this requirement
`[V]` (probe program calling `mrcore` outside the repository): it also
decodes 33-byte compressed points (`0x02` or `0x03` prefix) wherever it
decodes a point, so a compressed `S` passed to `Enrol` is accepted and
stored as given, and `kid` is then the hash of those 33 bytes. Do not
rely on this: a token whose `S` is not 65 bytes is not valid MR-1.

### 4.2 Scalars

A scalar is 32 bytes, big-endian. A freshly generated scalar (`c`, `e`,
and a new `s`) MUST be uniform in `[1, n-1]`. The reference
implementation draws 32 random bytes and rejects and redraws any value
that is zero or not less than `n` `[V]` (`TestRandomScalar*` in
`mrcore/scalar_test.go`, including a check that the test fails if the
upper bound is loosened). Before that was added it accepted anything
non-zero and relied on its curve library reducing values `>= n` modulo
`n` (`n + 1` gave `G`, `n` gave `O`), a bias of about `2^-32`; a
conforming implementation MUST NOT do that.

### 4.3 x-only results

An x-only ECDH result is the 32-byte x-coordinate of the shared point,
big-endian, exactly as returned by a platform ECDH primitive (the
"shared secret Z" of NIST SP 800-56A, with no key derivation applied).
Recorded as verified on a real StrongBox phone in the project's
`TESTREPORT.md`, item A4; not re-run for this document.

### 4.4 The volume secret and its hexadecimal form

`P` is 32 uniformly random bytes. MR-1 itself treats it as an opaque
byte string; Section 7 defines how it is generated and sealed.

In the LUKS2 application profile that the reference implementation
implements, the secret that is actually installed in the LUKS keyslot
and later handed to the password agent is not `P` but

    keyMaterial = hex(P)

that is, 64 lowercase ASCII hexadecimal characters `[I]` (read: `hex.EncodeToString`
in `cmd/unlocker/cryptsetup.go`, `luksKeyMaterial`). Enrolment installs
`keyMaterial`, and recovery delivers `keyMaterial` computed from the
recovered `P`; the raw `P` is never installed or delivered. This is a
property of the application, outside the MR-1 core: `mrcore` returns the
raw `P`.

Why: systemd's ask-password protocol was observed to cut an answer off
at its first NUL byte, delivering a shorter, wrong key even though
`cryptsetup` handles the same bytes correctly when given them in a file.
A uniformly random 32-byte string contains at least one zero byte with
probability `1 - (255/256)^32`, about 11.8 per cent (roughly one
enrolment in eight or nine). Hexadecimal has no NUL by construction.
The project's `AGENTS.md` records this as reproduced standalone with
`cryptsetup` and `systemd-cryptsetup` only; I did not reproduce it for
this document `[U]`. The 64-character form carries the same 256 bits.
An implementation for a different application MAY use `P` directly.

### 4.5 JSON byte strings

Both the token (section 6) and the wire messages (section 9) are JSON
documents in which every field holding bytes is a JSON string containing
`b64(...)` of the bytes: standard alphabet, with padding `[V]` (values
produced by `mrcore` in section 10.4; decoding checked with Go's decoder:
padding required, URL-safe alphabet refused). Implementations MUST emit
exactly this form and SHOULD refuse input in any other base64 variant.

The test-vector file (section 10) is the only place hexadecimal is used
for these values; hexadecimal there is a presentation choice for a
test file, not part of the protocol. The existing document
`docs/mr1-protocol.md` does not state the JSON encoding of byte fields
at all.

JSON text is UTF-8. Key names are case-sensitive for producers and
spelled exactly as in this document. The reference decoder is
case-insensitive on key names and ignores unknown keys in messages
`[V]` (probe: `{"KID":...,"x":...,"extra":1}` decodes into a
`RecoverRequest`). A conforming sender MUST NOT depend on either.

## 5. Key derivation and encryption

### 5.1 Key derivation

    k = HKDF(ikm = x(K), salt = kid, info = "mr-1 enrol", L = 32)

- `ikm` is the 32-byte x-coordinate of the shared point. Only the
  x-coordinate is used, so that a key holder that can return only
  x-coordinates (section 8.3) yields the same key as one that returns
  full points.
- `salt` is `kid`, 32 bytes. This binds the key to the key holder and
  keeps keys for different key holders independent even if a value of
  `K` were ever reused.
- `info` is the 10 ASCII bytes `mr-1 enrol` (hex `6d722d3120656e726f6c`),
  with no terminator. It is the same string at enrolment and at every
  recovery; it is a domain-separation constant and does not mean that a
  recovery is an enrolment.
- `L = 32`: a single HMAC block, so `k = HMAC(PRK, info || 0x01)` with
  `PRK = HMAC(salt, ikm)`.

`kid = SHA-256(S)` where `S` is the exact byte string stored in the
token (65 bytes in a conforming token).

### 5.2 Authenticated encryption

AES-256-GCM (NIST SP 800-38D) with:

- key `k` (32 bytes),
- a 12-byte nonce, drawn uniformly at random at enrolment,
- empty associated data,
- the 16-byte tag appended to the ciphertext, so a 32-byte `P` gives a
  48-byte `ct`.

`k` is fresh for every enrolment, because `c` is, so a random 96-bit
nonce under a key used exactly once cannot collide. The associated data
is empty; `kid`, `S` and `C` are not authenticated by the AEAD directly
but are bound through `K` and the salt, and altering any of them makes
decryption fail (section 12.5).

Decryption MUST check that the nonce is exactly 12 bytes before calling
the cipher: a corrupt token otherwise crashes some libraries, which
panic rather than return an error. The reference implementation does
this `[V]` (`mrcore/mr1.go`; probe call with a 1-byte nonce returns the
error `invalid nonce length 1, want 12`). `mrcore` does not check the
length of the recovered plaintext, since it treats `P` as opaque; an
application that expects 32 bytes MUST. The reference LUKS application
does (`checkSecretLength` in `cmd/unlocker/agent.go`, `[V]` by its unit
test).

## 6. The token

The token is a JSON object stored by the machine. In the LUKS2
application it is a LUKS2 token of type `mr-1`, so the object is also a
valid LUKS2 token document. Nothing in it is secret.

| Field | JSON type | Meaning |
|---|---|---|
| `type` | string | The constant `"mr-1"`. |
| `version` | integer | The constant `1` for this specification. The reference encoder does not force it, so a `Token` built with another version writes a document that no reader accepts `[I]`. |
| `keyslots` | array of strings | LUKS2 keyslot identifiers (decimal numbers as strings) holding the volume keys. Required by LUKS2 even when empty; not part of MR-1. A writer MUST emit `[]` rather than `null` or omit it; the reference encoder substitutes `[]` for a nil list `[V]` (`TestTokenAlwaysEncodesAKeyslotsArray`). |
| `recipients` | array of Recipient | One entry per enrolled key holder. The reference encoder writes a nil list as `null`, so a writer SHOULD emit `[]` `[I]`. |
| `machine` | object | `{"name": string, "transport_id": string}`: a label for the machine and its transport identity, shown to a key holder before approval. |

Recipient:

| Field | JSON type | Meaning |
|---|---|---|
| `kid` | b64 string | `SHA-256(S)`, 32 bytes. |
| `S` | b64 string | The key holder's public point, 65 bytes. |
| `C` | b64 string | The enrolment ephemeral public point `c.G`, 65 bytes. |
| `ct` | b64 string | `AEAD-Seal(k, nonce, P)`, 48 bytes. |
| `nonce` | b64 string | 12 bytes. |
| `transport` | any JSON, optional | Opaque to MR-1. The reference implementation stores `{"device_id": <string>}`, the key holder's transport identity, so the machine can tell which recipient it is talking to. |

Reading rules. A reader MUST check `type` and then `version` before it
interprets any other field and MUST reject a document with a different
type or a version other than 1, so that tokens belonging to other tools
sharing a LUKS2 header are refused by their type alone `[V]`
(`mrcore/token_golden_test.go`). A reader SHOULD ignore, and a writer
that rewrites the token MUST preserve, unknown top-level fields, so a
newer producer's data survives a round trip `[V]` (same test). The
reference implementation preserves unknown top-level fields only; unknown
fields inside a recipient or `machine` are discarded on rewrite `[I]`
(read from the struct definitions, not run).

Number of keyslots and recipients: the reference implementation
maintains one keyslot per recipient and refuses to guess when the counts
differ `[I]` (`cmd/unlocker/enrol.go`); that check is application
policy and not part of MR-1.

Several recipients may share one token. Each recipient is enrolled
independently with its own random `P` (section 7), so recovering through
one recipient yields that recipient's `P`, which opens that recipient's
keyslot and no other `[I]` (`cmd/unlocker/enrol.go` calls `Enrol` once
per recipient and adds one keyslot per call).

## 7. Enrolment

Input: the key holder's public point `S` (65 bytes), obtained by some
channel the machine trusts for the purpose (pairing, which is out of
scope).

1. Validate `S`: it MUST be a valid curve point and not `O`.
2. Draw `P`: 32 random bytes.
3. Draw `c`, a scalar (section 4.2). Compute `C = c.G` and `K = c.S`.
4. Compute `kid = SHA-256(S)` and `k = HKDF(x(K), kid, "mr-1 enrol", 32)`.
   If `K = O` the derivation is undefined and enrolment MUST fail;
   `K = O` happens only if `S = O` `[V]` (reference: `Enrol` with
   `S = 0x00` fails with "point at infinity").
5. Draw a 12-byte `nonce`. Compute `ct = AEAD-Seal(k, nonce, P)`.
6. Emit the recipient `{kid, S, C, ct, nonce}`, and give `P` to the
   application. Wipe `c`, `K` and `k`.

The machine then performs its application step (for LUKS2: add
`hex(P)` as a new keyslot, section 4.4) and stores the recipient in the
token. `P` is never stored.

Enrolment needs only `S`, so it can be done offline and without the key
holder's participation once `S` is known.

## 8. Recovery

The machine holds the token. It selects a recipient (the reference
implementation matches the verified transport identity of the
connecting peer against the recipient's `transport.device_id`; other
selection rules, such as trying each recipient by `kid`, are possible
and not specified here).

### 8.1 Machine: start

1. Draw `e`. Compute `E = e.G` and `X = C + E`, where `C` is the
   recipient's stored point.
2. Send `RecoverRequest{kid, X}` (section 9).

`X` is uniformly distributed to anyone who does not know `C`'s discrete
logarithm: `E` is a fresh uniform point that blinds `C`. This is what
keeps `K` and `P` from the key holder and from an eavesdropper.

### 8.2 Key holder: answer

1. Look up the private scalar for `kid`. If there is none, or the human
   operator declines, reply with `RecoverResponse{error}` and stop.
   The reference laptop key holder does not do either: it holds one
   scalar, ignores `kid`, and closes the channel with an error instead of
   sending an `error` response `[I]` (`cmd/keyholder/unlock.go`). The
   phone also ignores `kid`, which it keeps for display only `[I]`
   (`mobile/mobile.go`). Only the
   phone sends one `[I]` (`mobile/mobile.go`). A machine MUST therefore
   treat a closed channel as a failed recovery.
2. Validate `X`: it MUST be non-empty, MUST NOT be `O` (the single byte
   `0x00`), and MUST be a valid curve point. Refuse otherwise. The
   point is chosen by the peer and the key holder is about to apply its
   long-term secret to it, so this is the one check that stands between
   an attacker and a private-key operation on attacker-chosen input
   (section 12.2). In the reference implementation this is
   `ValidateChallengePoint`. Note that it accepts any encoding the group
   decodes, including compressed `[V]`; a stricter key holder MAY
   require 65-byte uncompressed `X`.
3. Compute `Y = s.X`, and reply in one of two forms:
   - **full point:** `RecoverResponse{Y}`, `Y` as a 65-byte point. For
     a key holder that holds `s` in the clear.
   - **x-only:** `RecoverResponse{xOnly}`, `xOnly = x(Y)`, 32 bytes. For
     a key holder whose key is in hardware that exposes ECDH only.

Because `X = C + E`, `Y = s.C + s.E = c.S + e.S = K + e.S`. The machine
knows `e` and `S`, so it can subtract `e.S`.

### 8.3 Machine: finish

Receive the response. If `error` is non-empty the key holder declined;
the machine stops. Otherwise let `eS = e.S` (using `S` from the stored
recipient).

**Full point.** Take `Y` from the response (validated as a curve point)
and compute `K' = Y - eS`. Compute `k' = HKDF(x(K'), kid, "mr-1 enrol", 32)`
and `P = AEAD-Open(k', nonce, ct)`. A failure of the authentication tag
means the answer did not come from the right key holder (or the token is
damaged), and recovery fails.

**x-only.** Take `xOnly` (it MUST be exactly 32 bytes and the
x-coordinate of a point on the curve). Compute the two points with that
x-coordinate: `Y0` with an even y and `Y1` with an odd y (parity is the
least-significant bit of y; these are the points SEC1 compressed
prefixes `0x02` and `0x03` select, and they are negations of each
other). The correct point is not known, because the hardware did not
return y. For each candidate in the order `Y0`, `Y1`:

1. `K' = candidate - eS`. If this is `O` the candidate fails.
2. `k' = HKDF(x(K'), kid, "mr-1 enrol", 32)`.
3. Try `AEAD-Open(k', nonce, ct)`. On success, `P` is the plaintext;
   stop.

If both candidates fail, recovery fails. The authentication tag
identifies the right candidate: the wrong one gives `K'' = -Y - eS`,
whose x-coordinate differs from that of `K` with overwhelming
probability, so `k''` differs and the tag check fails except with
probability about `2^-128`. The vector in section 10 needs the second
candidate: its `Y` has an odd y, so the even candidate fails first
`[V]`. An implementation MUST NOT infer the parity itself or try only
one candidate.

On both paths the machine MUST wipe `e`, `eS`, `K'` and `k'` after use,
and MUST report an authentication failure as an error rather than
crashing.

### 8.4 Machine: report

After it has received a response that was not an error, the machine
sends `RecoverResult` (section 9) to say whether the recovered secret
was usable, whatever the outcome. This is an application-level
acknowledgement and not part of the cryptography; its reason is that
without it a key holder can only know its write succeeded, not that a
volume unlocked (section 9.2).

## 9. Wire messages

### 9.1 Framing

Each message is

    length (4 bytes, unsigned, big-endian) || body

where `body` is a JSON document (UTF-8) and `length = len(body)`. The
maximum body length is 65536 bytes. A sender MUST NOT send a longer
body, and a receiver MUST reject a larger declared length before
allocating anything `[I]` (`mrcore/wire.go`: `maxMessageSize`; the
check precedes the allocation). A short read of either part is an
error. The channel is a reliable byte stream; the framing is the only
delimiter.

### 9.2 Messages and sequence

Roles: M is the machine, K the key holder. All messages are sent over
the authenticated channel.

| # | Direction | Message | Body fields |
|---|---|---|---|
| 1 | M to K | `Hello` | `name` (string): the machine's name, to be shown to a human before they approve. |
| 2 | M to K | `RecoverRequest` | `kid` (b64), `X` (b64, 65 bytes). |
| 3 | K to M | `RecoverResponse` | exactly one of `Y` (b64, 65 bytes), `xOnly` (b64, 32 bytes), `error` (string). |
| 4 | M to K | `RecoverResult` | `ok` (boolean), `error` (string, omitted when empty). |

Fields documented as omitted when empty are omitted from the JSON, not
sent as `null` or `""` `[V]` (probe: `{"xOnly":"..."}`, `{"ok":true}`);
`name` is always present.

Rules, from the reference machine (`cmd/unlocker/agent.go`) and key
holder (`cmd/keyholder/unlock.go`) `[I]` (read, and the machine and key
holder exercised together by the project's test suite, which this
document did not run separately):

- The key holder shows `Hello.name` and the machine's verified transport
  identity to a human and waits for approval before message 3 (it may
  ask for approval before or after reading message 2; only the order of
  messages on the wire is fixed).
- In message 3, exactly one of the three fields is set. The reference
  machine tests `error` first (declined), then `Y`, then `xOnly`; if
  both `Y` and `xOnly` were set it would use `Y`, and a response with
  none of the three is a failure. A conforming key holder sets exactly
  one.
- Message 4 is sent after every message 3 that was not an error,
  whether recovery succeeded (`ok` true) or failed (`ok` false, `error`
  saying why, for example a secret that did not open the volume). It is
  not sent if the key holder declined, nor if the exchange failed
  earlier. It exists because a relayed session can die in such a way
  that a write succeeds and is delivered nowhere; the key holder must
  not tell a human "unlocked" because of a successful write alone. It
  is the last message; the reference machine then waits up to three
  seconds (`resultLingerTimeout`) for the key holder to close the
  channel first, so that the verdict is not lost when closing tears down
  a relay session `[I]`.
- Either side that finds the other's behaviour inconsistent with this
  table closes the channel. The reference machine bounds one whole
  exchange by a timeout (three minutes, `exchangeTimeout`, `[I]`) and
  closes the connection when it expires.

Out of scope: three further messages (`PairChallenge`, `PairRequest`,
`PairResult`) belong to the pairing step in which a key holder's `S`
reaches the machine. They use the same framing and are defined in
`mrcore/pair.go`; this document does not specify them.

## 10. Test vectors and a worked example

The file `testdata/mr1.json` in this repository holds one complete
example. All values there are hexadecimal. The scalars are throwaway
test values and are not secret. The values are reproduced here so the
document stands alone; where this table shows a value that the file
does not contain it is marked "derived" and was computed from the
others, see section 11.

### 10.1 Inputs and enrolment

    s     = 111111111111111111111111111111111111111111111111111111111111114b
    S     = 04 4713908c3978c1c8f386280baf141825bde527d265583908f01de395b93ec53c
               575334d08042067a54e5a45796e2c78cd15d4c8f147ee56671d1d9305a608ee5
    c     = 2222222222222222222222222222222222222222222222222222222222222278
    C     = 04 2a7ed6b8427a8741cbfef4030c76a169b46933740e3d13d548a7259715e4e5ce
               bffe3ec76a99b646bbb8018819688f828c46fac2db07b1b9ef14966d6b5d335f
    kid   = SHA-256(S)
          = 06cad1f5f40bcad0764700bd5d7bc369153d9a191034b3cd3de1db54984bb44c
    P     = 6d722d31207465737420766563746f7220766f6c756d65207365637265742121
            (ASCII "mr-1 test vector volume secret!!")
    nonce = 444444444444444444444444

Derived (not in the file): `K = c.S`, 

    K     = 04 16b2de3dbb082f98e4470b6ac8bb31f28fb3b9f62b62f8e27db1304c6184eadc
               b006b3fe6fea2928e484ad19a0f55a52d0ff2ccaa042e9c016579fd4d49ee45b
    x(K)  = 16b2de3dbb082f98e4470b6ac8bb31f28fb3b9f62b62f8e27db1304c6184eadc
    k     = HKDF(x(K), kid, "mr-1 enrol", 32)
          = f15e5f2bcd06fdbd2a4efcab35a37fc3fd8b992f356cb0176742049a4f3a1678

(`k` is an intermediate of this published throwaway example, listed so
an implementer can find the first diverging step. It protects nothing
real.)

    ct    = AEAD-Seal(k, nonce, P)
          = 2412c61b023ccfe648e41ddfa7911877e62660f78a369b4059df8a76d969be5b
            17549c80584734ed69451fc2880fd932      (32 + 16 bytes)

### 10.2 Recovery

    e     = 3333333333333333333333333333333333333333333333333333333333333369
    E     = 04 21d8fa836025858e6cc93f7f0c79b1b1ce8de662a94b629e4643a7785107b886
               0feabd09acb4b1f21cd219629700df2bcf32ff092b4b04c68223f0ffceb16775
    X     = C + E
          = 04 22dbe49652e74fdc63bee50e83901685a64ec408f7fe2b568933bf08307077ec
               f3ecb93789bccb7580166bcd0302bb5e30ba6cf50e8c4f585339a8eb9b011175
    Y     = s.X
          = 04 4cdeeffdbf221ceeee0a92ebf2916d60e7e84302bf3b64497a44c1eade5b579d
               acffc4328af20802c79aa958abf5556c92b144e677dbdf765a83684b973d96a5
    xOnly = x(Y)
          = 4cdeeffdbf221ceeee0a92ebf2916d60e7e84302bf3b64497a44c1eade5b579d

Derived (not in the file):

    eS    = e.S
          = 04 f9b862a20adfe9931aca0ca2df10a9415b83fcd54149c50b1ad78913d9dab700
               4bb293765a9111226807d5a820db9f3cd2985209087030fb624f53d866fe30f8
    Y - eS = K                                           (full-point path)

x-only path, trying both candidates for the point with x-coordinate
`xOnly`:

- Even-y candidate: `x(K')` is `5b3e472a92bf5f6516df990806ca042f0c83009172e3df40fda9adae6e58cd0d`,
  `k'` is `1a74f67882d8b5c78506144008732c61e20abdcb8484ded323acc44b761dcd80`;
  the tag check fails.
- Odd-y candidate (the real `Y`; its last byte `a5` is odd): `x(K')` is
  `16b2de3dbb082f98e4470b6ac8bb31f28fb3b9f62b62f8e27db1304c6184eadc`
  which equals `x(K)`; `k'` equals `k`; the tag check succeeds and
  yields `P`.

### 10.3 The token for this example

With machine name `study desktop`, machine transport identity
`MACHINE-DEVICE-ID`, one keyslot `"1"`, and the recipient's transport
identity `KEYHOLDER-DEVICE-ID`, the reference implementation writes
(one line, shown wrapped; key order is lexicographic at top level)
`[V]` (probe program calling `mrcore`'s token encoder with these values):

    {"keyslots":["1"],"machine":{"name":"study desktop","transport_id":"MACHINE-DEVICE-ID"},
     "recipients":[{"kid":"BsrR9fQLytB2RwC9XXvDaRU9mhkQNLPNPeHbVJhLtEw=",
     "S":"BEcTkIw5eMHI84YoC68UGCW95SfSZVg5CPAd45W5PsU8V1M00IBCBnpU5aRXluLHjNFdTI8UfuVmcdHZMFpgjuU=",
     "C":"BCp+1rhCeodBy/70Awx2oWm0aTN0Dj0T1UinJZcV5OXOv/4+x2qZtka7uAGIGWiPgoxG+sLbB7G57xSWbWtdM18=",
     "ct":"JBLGGwI8z+ZI5B3fp5EYd+YmYPeKNptAWd+KdtlpvlsXVJyAWEc07WlFH8KID9ky",
     "nonce":"RERERERERERERERE","transport":{"device_id":"KEYHOLDER-DEVICE-ID"}}],
     "type":"mr-1","version":1}

### 10.4 Wire frames for this example

Each frame is the 4-byte length then the body. `[V]` (produced by
`mrcore.WriteMessage`).

Message 1, `Hello`, 24 bytes:

    00000018 7b226e616d65223a227374756479206465736b746f70227d
    {"name":"study desktop"}

Message 2, `RecoverRequest`, 149 bytes (`00000095`), body:

    {"kid":"BsrR9fQLytB2RwC9XXvDaRU9mhkQNLPNPeHbVJhLtEw=","X":"BCLb5JZS50/cY77lDoOQFoWmTsQI9/4rVokzvwgwcHfs8+y5N4m8y3WAFmvNAwK7XjC6bPUOjE9YUzmo65sBEXU="}

Message 3, x-only form, 56 bytes (`00000038`), body:

    {"xOnly":"TN7v/b8iHO7uCpLr8pFtYOfoQwK/O2RJekTB6t5bV50="}

Message 3, full-point form, 96 bytes (`00000060`), body:

    {"Y":"BEze7/2/Ihzu7gqS6/KRbWDn6EMCvztkSXpEwereW1edrP/EMoryCALHmqlYq/VVbJKxROZ32992WoNoS5c9lqU="}

Message 4, success, 11 bytes:

    0000000b 7b226f6b223a747275657d
    {"ok":true}

## 11. Validation of this specification

`[V]` for the revision of this text that was committed with it. A
throwaway script outside the repository, using Python's standard
library for SHA-256 and HMAC, a hand-written P-256 implementation over
Python integers (no elliptic-curve library), and the `cryptography`
package's AES-256-GCM, with no use of `mrcore`, parsed the hexadecimal
values out of section 10 of this file, recomputed `S`, `C`, `kid`, `K`,
`k`, `ct`, `E`, `X`, `Y`, `xOnly` and `eS` from the scalars using only
the definitions in sections 2 to 8, ran the x-only trial (the even
candidate failed the tag check, the odd one recovered `P`), rebuilt the
token JSON and the six wire frames, and compared everything with this
file and with `testdata/mr1.json`. All checks passed. A copy of the
file with one hexadecimal digit of `ct` altered made the script fail, so
the comparison is not vacuous. The script is not kept in the repository;
re-run the check rather than trusting this paragraph for any later
revision of the text `[U]`.

Limits of that check: the script's author also read the Go code and
wrote the first draft of this text, so it is not an implementation by a
second person, and it cannot find an error that both share. It does
show that the text, taken literally, determines the vectors. The
failure paths (malformed points, bad nonce lengths, truncated frames)
are not covered by the vectors and were not reproduced `[U]`.

## 12. Security considerations

No formal security proof of MR-1 exists `[U]`. The statements below are
the reasoning behind the design, not theorems.

12.1 **The channel.** MR-1 assumes an authenticated, confidential
channel to the right peer. It does not authenticate the key holder to
the machine: a fake key holder obtains a wrong `Y` and the AEAD tag
fails. It does not authenticate the machine to the key holder either;
the human approval step and the transport identity do that. The
reference transport is TLS 1.3 with peers pinned by the hash of their
certificates (Syncthing Device IDs), self-signed, with no certificate
authority. `Hello` and the result are not authenticated by MR-1 and
rely on that channel.

12.2 **The key holder is a private-key oracle for points the machine
chooses.** It computes `s.X` for `X` supplied by the peer. It MUST
reject `X` that is `O`, malformed, or not on the curve before
multiplying, which is what stops invalid-curve attacks; on P-256, whose
cofactor is 1, there is no small-subgroup concern. A hardware ECDH API
usually validates the peer point itself, but a key holder SHOULD still
check, so the operation is refused visibly rather than silently
performed. In the reference implementation, `ValidateChallengePoint`
rejects empty input, `O`, and anything that does not decode to a curve
point. Both key holders call it (`cmd/keyholder/unlock.go`,
`mobile/mobile.go`; `[I]`, from reading). The phone calls it as soon as
the request is read, before the point is kept or shown to the Kotlin
side, and again inside the answer step `[V]`
(`TestConnectRefusesAnOffCurveChallengePoint`); until that was added it
checked only inside the answer step, after the Keystore had already been
used. Its hostile-input test,
`TestValidateChallengePointRefusesHostileInput`, passes `[V]`.

12.3 **What blinding protects, and what it does not.** The key holder
sees only `X = C + E`, in which the uniformly random `E` hides `C`; it
learns neither `K` nor `P` from the exchange. An eavesdropper sees `X`
and `Y` but not `e`, and cannot compute `eS`, so cannot compute `K`.
However, a key holder that also obtains a copy of the token holds `C`,
`kid`, `nonce` and `ct`, can compute `K = s.C`, and can decrypt `P`
offline. The scheme therefore protects against a key holder that has
only seen the exchange, not against one that has also had the disk.
The same holds for Tang.

The same applies to anyone who holds the token and the machine's
transport identity, which sit on the unencrypted disk together. Such a
party can present the machine's Device ID to a key holder, send any `X`
it likes (including the unblinded `X = C`, that is `e = 0`), and finish
the recovery exactly as the real machine would `[I]` (an independent
reviewer ran the algebra; it was not re-run here). Blinding hides `C` from the key holder; it does
nothing against a machine impersonator. One approval from the human is
therefore enough to hand over `P`, and that approval need not be careless:
if the impersonator is listening while the owner reboots the real machine,
both announce the same Device ID, the key holder takes the first handshake
that pins correctly, and the owner may approve the wrong one `[I]`.
Revoking the key holder afterwards does not help, because the keyslot in
the copied image still opens with `P`. The design's defence is a human in
the loop for every recovery, not cryptography; the defence that would
survive a disk image is sealing the machine's identity in a TPM, which is
not built (`docs/threat-model.md`).

12.4 **Replay.** `Y` depends on `X`, which depends on a fresh `e` drawn
for each attempt; a recorded answer is useless for a later attempt.

12.5 **Unauthenticated token fields.** AEAD associated data is empty.
Substituting `C`, `S` or `kid` in the token changes `K` or the salt and
makes decryption fail; this is a denial of service for whoever can
write the token, which is not an additional capability if they could
simply delete it. Tampering with `ct` or `nonce` fails the tag.

12.6 **No forward secrecy for `P`.** `P` is fixed at enrolment and is
protected by the key holder's long-term `s`. A later compromise of `s`
together with an old copy of the token recovers `P`. The project's
documents describe a threshold variant as unbuilt `[I]`.

12.7 **Randomness.** `P`, `c`, `e` and the nonce MUST come from a
cryptographically secure generator. Reuse of `c` across recipients is
not specified and MUST NOT happen: the reference implementation draws a
fresh `c` and a fresh `P` per `Enrol` call.

12.8 **Secret handling.** Implementations should wipe `c`, `e`, `eS`,
`K`, `k`, `P` and any derived passphrase after use, including on error
paths. The reference implementation does so on a best-effort basis; Go
offers no guarantee that a write to memory is not optimised away `[I]`
(its own comment on `Wipe` says so).

12.9 **No confirm code.** An earlier version had the machine print a
code on its console and send that same code to the key holder, which
had to hand it back before the machine sent its recovery request. It
proved nothing: the code travelled to the key holder in the clear (over
the authenticated channel), so any key holder could return it without
anyone seeing the console, and a party with a copy of the disk could
simply not ask. It was removed before any release. A second factor
worth having would need more than one key holder's answer to recover
`P` (a threshold), which section 12.6 and `docs/threat-model.md` list as
not built.

12.10 **Denial of service.** The 64 KiB message cap bounds memory per
message. Nothing in the protocol bounds the number of connections or
requests; that is up to the transport and the application. The reference
transport bounds each TLS handshake to ten seconds on both sides, so a
peer that connects and goes silent cannot hold a listener, and bounds the
wait for a pairing request to thirty seconds `[V]`
(`TestServerHandshakeGivesUpOnASilentPeer`,
`TestPairGivesUpOnAPeerThatNeverSendsItsRequest`). Before that a single
silent connection could take the machine off the air for the rest of the
boot. An attacker can still occupy a listener for the length of one
handshake timeout at a time `[I]`.

12.11 **Side channels.** The reference implementation uses a
constant-time P-256 library for scalar multiplication, including in
`Negate`, which it implements as multiplication by `n - 1` and not as
big-integer arithmetic `[I]`. Side channels in a platform's hardware
ECDH are out of scope.

12.12 **Version and algorithm agility.** There is none beyond
`version`. A change of curve, KDF or AEAD requires a new `version` and a
new `info` string. The reference code anticipates a `Group` abstraction
for another curve but implements only P-256 `[I]` (`mrcore/group.go`).

12.13 **Known gaps `[I]`.** These are not specified because the reference
code does not decide them in `mrcore`: how a machine chooses which
recipient to contact when a token has several; what a receiver does with
a point that is not 65 bytes (section 4.1 binds emitters only); which
token fields are optional on read (the reference decoder's treatment of
absent fields was not examined `[U]`); the handling of a declared
frame length of 0 (an empty body is not valid JSON, so the reference
reader fails at decoding). An
independent implementation SHOULD NOT rely on any of these.

## 13. Differences from Tang (McCallum-Relyea)

The algebra is the same: the client blinds with a fresh ephemeral point,
the server multiplies by its private scalar, and the client unblinds by
subtracting the multiple of the server's public key. The comparison
with Tang and Clevis below is from the author's recollection of those
projects and has not been checked against their current source or
specifications in this session `[U]`.

- **Responses.** Tang's server returns a full point. MR-1 also accepts
  an x-only response and resolves the sign by trial, so the key holder
  can be a non-exportable hardware key that offers only ECDH.
- **Who is the server.** Tang is an unauthenticated HTTP service that
  holds exportable keys and answers anyone who can reach it. An MR-1 key
  holder is a human-operated device (a phone, a laptop), authenticated
  by transport identity, that answers one request at a time and can gate
  each answer on a person.
- **Curve and encodings.** Tang and Clevis default to larger NIST curves
  and use JOSE structures (JWK, JWE). MR-1 uses P-256, raw SEC1 points,
  and a plain JSON token with standard base64 strings `[I]`.
- **Key derivation.** The Clevis/JOSE stack derives its content key with
  the JOSE key-agreement KDF; MR-1 uses HKDF-SHA-256 with `kid` as salt
  and a fixed `info` string `[I]`.
- **Key identifier.** Tang identifies keys by a JWK thumbprint. MR-1's
  `kid` is `SHA-256` over the 65-byte encoded public point `[I]`.
- **Binding to storage.** Clevis stores a JWE in a LUKS2 token and
  one secret per binding. MR-1 stores one recipient per key holder in a
  single token, each with its own secret and keyslot.
- **Transport.** Tang is HTTP. MR-1 leaves the channel to the
  application; the reference one is TLS 1.3 pinned to Device IDs over a
  relay network or LAN multicast discovery.
- **Unattended boot.** Tang's design target; MR-1 deliberately has a
  human in every recovery.

## 14. Disagreements between this specification and `docs/mr1-protocol.md`

This specification follows the code (`mrcore/*.go`, and the callers in
`cmd/` where the sequence of messages is decided). `docs/mr1-protocol.md`
differs as follows. Each item is `[I]`, established by reading both texts
against the code; none of them is a cryptographic disagreement.

1. **"Any one of them can recover P."** The document says several
   recipients can share one token and that any one of them can recover
   `P`. In the code each `Enrol` call draws its own `P` and the
   application adds a separate keyslot for it, so recipients share a
   token, not a secret; recovering through a recipient yields that
   recipient's own `P`. (`docs/threat-model.md` likewise describes
   sharing `P` across recipients as unbuilt future work.)
2. **Where hexadecimal belongs (wording only).** The document already
   says the keyslot gets the hex form and cites `luksKeyMaterial`, but
   its enrolment step 1 reads as though the protocol hex-encodes `P`. In
   the code `mrcore.Enrol` returns raw `P`; the hexadecimal form is
   applied by the LUKS application (`luksKeyMaterial` in `cmd/unlocker`).
3. **Gaps rather than conflicts.** The document does not state the JSON
   encoding of byte fields (standard padded base64), the message-size
   limit (65536 bytes), that `X` and `Y` are validated, that
   `RecoverResponse` fields are mutually exclusive, the token's `version`,
   `keyslots` and `machine` fields, the 12-byte nonce check,
   that the sign candidates are tried even first and odd second, or that
   the GCM associated data is empty. This specification states each.
4. **Test vector file comment.** `testdata/mr1.json` says "every value is
   hex-encoded". That is true of the file; the token and the wire use
   base64 (section 4.5).
