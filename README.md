# synctang

An on-demand LUKS volume unlocker for dracut-based boots. A machine asks;
a human, on a laptop or phone, answers. There is no always-on server: the
key holder is not listening for the machine to say hello, it is on demand,
full stop. Anyone who needs unattended reboots should use vanilla Tang;
this project does not accommodate that use case, on purpose. See
"Threat model" below for why.

Status: work in progress, see `STATUS.md`.

## Components

- `mrcore`: the MR-1 protocol (enrol, recover, token format) and the
  transport interface, as a Go package shared by everything else.
- `unlocker`: the machine-side binary. Runs in the dracut initrd and,
  after boot, as a systemd password agent. Subcommands: `enrol` (add a
  recipient, or `--remove`/`--replace` one), `agent`, and `status`.
  `pair` is planned, not built.
- `keyholder`: the Ubuntu laptop CLI. Subcommands: `init`, `unlock`,
  `export-pubkey`. `pair` is planned, not built.
- Android app (`android/`): the phone key holder. Pairs by QR code, unlocks
  behind `BiometricPrompt`, keeps its key in the Android Keystore.
- `dracut/90unlocker`: the dracut module.

## Quick start

Filled in as each component reaches a runnable state; see `STATUS.md` for
what is not here yet (both `pair` subcommands).

### keyholder (laptop), file backend

```
go run ./cmd/keyholder init
go run ./cmd/keyholder export-pubkey
```

`init` generates a new long-term MR-1 key and a separate persistent
transport identity, both under your config directory (`init` refuses to
overwrite an existing key without `--force`). `export-pubkey` prints the
resulting public key and transport ID, for pasting into `unlocker enrol`
below.

### unlocker (machine), enrolling a laptop key holder

```
echo -n 'your-existing-luks-passphrase' > /tmp/passphrase
go run ./cmd/unlocker enrol \
  --device /path/to/your/luks-device-or-image \
  --pubkey <S from export-pubkey above> \
  --recipient-transport-id <transport-id from export-pubkey above> \
  --existing-passphrase-file /tmp/passphrase \
  --name my-laptop-name
rm /tmp/passphrase
```

This adds a new LUKS2 keyslot holding a freshly generated secret, and a
`mr-1` token entry for that key holder. Run `cryptsetup luksDump
--device` to see it; `cryptsetup token export --token-id N` to see the
token JSON itself. `enrol` also prints this machine's own transport ID,
which a key holder needs to dial it.

### unlocker (machine), checking who can unlock it

```
go run ./cmd/unlocker status --device /path/to/your/luks-device-or-image
```

Prints this machine's transport ID, the name key holders see, and one
line per enrolled key holder: its transport ID, its keyslot, and a short
form of its key id. Read-only, including of this machine's own transport
identity: if none has been created yet, `status` says so rather than
creating one.

Worth running before `enrol --remove`, which takes a transport ID and
destroys the keyslot belonging to it: naming the wrong one revokes the
wrong key holder.

### Recovering (machine listening, laptop answering)

On the machine (or against a plain loopback image, with `--direct-addr`
bypassing discovery and the relay network entirely for a local test;
see "Transport" below for what runs instead when it is not given):

```
go run ./cmd/unlocker agent --device /path/to/your/luks-device-or-image \
  --name my-machine-name [--confirm-code] [--direct-addr 127.0.0.1:PORT]
```

On the laptop, once the machine above is waiting:

```
go run ./cmd/keyholder unlock <machine's transport id> [--direct-addr 127.0.0.1:PORT]
```

`unlock` shows the machine's name, waits for Enter (or `--yes`) to
approve, relays a confirm code back if the machine asked for one, and
answers once. `agent` verifies the recovered secret against the device
before ever answering systemd's password agent request, and keeps
listening for the next attempt until it receives `SIGTERM` (which
`dracut/90unlocker/unlocker-stop.sh` sends before pivoting to the real
root).

## Installing the Android app

The app is published from this repository as a small F-Droid repository, so
a phone gets installs and updates without anybody copying APKs around.

1. Install an F-Droid client (F-Droid, Neo Store, or similar).
2. Add this repository:

       https://muelli.github.io/synctang/fdroid/repo

   or open that page in a browser and scan the QR code on it. The page also
   shows the repository fingerprint; a client pins it when the repository is
   added.
3. Install "synctang key holder" from the repository.

Two things worth knowing before you do:

- **A release APK is signed with a different key than a locally built debug
  APK.** Android refuses to update an app when the signer changes, so a
  phone that already has a debug build must uninstall it first, and
  uninstalling destroys the Keystore key and with it that phone's
  enrolment on every machine. Re-enrol after switching.
- **The repository fingerprint is pinned at the moment you add it.** If the
  index signing key ever changes, re-scanning the QR code does not update an
  existing entry; the repository has to be removed and added again.

Publishing is automatic: a push to `main` publishes a release-candidate
build, and a `v<versionCode>` tag publishes a release. Both need the two
signing secrets described below.

### Setting up publishing (once)

1. Generate two seeds and store them as repository secrets
   `APP_SIGNING_SEED` and `FDROID_SIGNING_SEED`:

       openssl rand -base64 48

   Back them up somewhere durable. The seeds *are* the signing keys: lose
   them and the app cannot be updated, only reinstalled from scratch.
2. Run the `bootstrap-signing` workflow once. It derives both identities
   inside CI, so the seeds never leave the secret store, and opens a pull
   request adding the two public certificates under `signing/`.
3. Merge that pull request, then push to `main`. The `publish-fdroid`
   workflow builds, signs, generates the repository and pushes it to the
   `fdroid-repo` branch; GitHub Pages serves that branch.
4. Fill in `AllowedAPKSigningKeys` and the version fields in
   `fdroid/com.github.muelli.synctang.yml` before submitting to
   f-droid.org. That file is for the f-droid.org catalogue and is not read
   by the self-hosted repository.

Until the secrets exist the publish workflow skips itself with a warning
rather than failing, so it is harmless to merge before step 1.

## Boot-time options (dracut)

At boot the agent is started by `dracut/90unlocker/unlocker-start.sh`,
from dracut's `initqueue/settled` hook, alongside the ordinary console
passphrase prompt rather than instead of it: whoever answers first
wins, and the console keeps working either way. Two kernel command
line options control it:

- `rd.unlocker=0`: do not start the agent at all. The escape hatch for
  when the network path itself is what is broken and you just want the
  console prompt.
- `rd.unlocker.confirm_code=1`: start the agent in confirm-code mode
  (see "Threat model" below). Off by default, deliberately: it
  requires a human with eyes on that machine's console to read the code
  back, which the ordinary remote-unlock case does not have. Opt in per
  machine.

## Transport: local network and the Internet, at once

`unlocker agent` and `keyholder unlock` do not have to guess in advance
whether the Internet is reachable. Unless `--direct-addr` is given
(which bypasses both, for offline tests and manual same-network
pairing), both sides run two transports at once and use whichever
finds the other first:

- `transport.LocalDiscovery`: the machine multicasts its Device ID and
  a plain TCP listener's address on the local network every couple of
  seconds; the key holder listens for a matching announcement and
  dials it directly. Needs a LAN link and nothing else: no DNS, no
  route to the Internet, no third-party infrastructure.
- `transport.SyncthingRelay`: the public Syncthing relay network and
  global discovery, for when the two sides are not on the same
  network.

The Android app races the same two transports (`mobile/mobile.go`'s
`newDialTransport`), so a phone can unlock a machine on the same
network with no Internet at all. Android filters multicast out before
it reaches an application unless a `MulticastLock` is held, so the app
declares `CHANGE_WIFI_MULTICAST_STATE` (a normal permission, granted at
install time with no runtime prompt) and holds a lock for the duration
of a dial and no longer, since holding one keeps the Wi-Fi chip out of
its power-saving filter. A device that will not give out a lock loses
local discovery and keeps the relay: `MulticastLease` degrades rather
than breaks. Both sides must share a multicast domain, and local
discovery is IPv4 only.

`transport.Multi` races the two: on a LAN with no Internet route, the
relay path simply keeps failing in the background while local
discovery succeeds, so unlock keeps working; with the Internet
reachable, both are tried and whichever answers first wins, so this
costs nothing when local discovery is not needed at all. Either way
the TLS handshake and Device ID pinning that follow are identical: an
announcement (local or global) is only ever a hint about where to
dial, never trusted on its own, exactly as the plan's threat model
requires (see below).

## Protocol: MR-1

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
     `x(s.X)`, 32 bytes, never the full point. `[V]` on an emulator's
     non-StrongBox Keystore backend, see `docs/wp0-keystore-ecdh.md`;
     `[U]` on real StrongBox hardware, pending the retest described
     there.
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
unattended reboots. See "Unattended boot" below.

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
recovery request; see "Threat model" for why.

## Threat model

**Disk thief during an unlock window.** Whoever steals the machine has
the LUKS2 token and the machine's transport identity: both live on the
unencrypted disk by design (the header is useless without a key holder,
per MR-1's properties above, but the thief can still *ask*). Mitigations,
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
StrongBox, biometric-gated per use; `[V]` on an API 34 emulator's
non-StrongBox backend that Android Keystore accepts a manually
constructed peer point and returns the correct raw x-coordinate (see
`docs/wp0-keystore-ecdh.md`); `[U]` on real StrongBox hardware, pending a
human with a suitable phone.

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

## Rotation and revocation

**Revocation**: `unlocker enrol --remove <recipient-transport-id>`
destroys that recipient's own LUKS2 keyslot (`cryptsetup luksKillSlot`)
and removes its entry from the token, leaving every other recipient's
keyslot and token entry untouched:

```
echo -n 'your-existing-luks-passphrase' > /tmp/passphrase
go run ./cmd/unlocker enrol --device /path/to/your/luks-device-or-image \
  --remove <recipient's transport-id, from keyholder export-pubkey> \
  --existing-passphrase-file /tmp/passphrase
rm /tmp/passphrase
```

`[V]` `cmd/unlocker/enrol.go`'s `removeRecipient`; the revoked
recipient's own recovered secret genuinely stops opening the device,
not merely dropped from the token.

**Rotation** replaces one key holder's key in a single command:

```
echo -n 'your-existing-luks-passphrase' > /tmp/passphrase
go run ./cmd/unlocker enrol \
  --device /path/to/your/luks-device-or-image \
  --replace <the key holder's transport-id> \
  --pubkey <its new S, from keyholder export-pubkey after rotating> \
  --existing-passphrase-file /tmp/passphrase
rm /tmp/passphrase
```

The new key is enrolled before the old one is revoked, so the key
holder can open the volume throughout; the old key stops working when
the command returns. The rotated entry keeps its transport identity
unless `--recipient-transport-id` says otherwise, since a key holder
that rotates its MR-1 key normally keeps the identity it dials with.

`[V]` `cmd/unlocker/enrol.go`'s `replaceRecipient`, pinned by tests that
check the old key genuinely stops opening the device and the new one
starts, that other key holders are untouched, and that a typo in the
transport id changes nothing at all.

The laptop side of a rotation is `keyholder init --force`, which
generates a fresh MR-1 key but deliberately keeps the existing
transport identity, precisely so a rotation does not change the Device
ID every `AuthorizedPeers` list already carries. So a full rotation is:

```
go run ./cmd/keyholder init --force
go run ./cmd/keyholder export-pubkey    # new S, same transport id
# then unlocker enrol --replace on each machine, as above
```

`keyholder pair`/`unlocker pair` (live network pairing, as opposed to
copying a public key across by hand) are still not built.

## Unattended boot

Explicitly out of scope. This project trades unattended reboots for a
human-approved unlock, hardware-held key holder secrets, and a phone as a
first-class key holder. If unattended reboot is a requirement, use Tang
directly.

## Debian package reproducibility

Building the Debian packages (`just deb`) from the same git commit
produces byte-identical `.deb` files, verified rather than assumed: `just
deb-repro` builds twice, deliberately varying umask, `TZ` and `LC_ALL`
between the two builds, and compares the results with `sha256sum`. The
`build-deb` CI job runs this same check on every push and pull request.

This holds for a given commit under: the exact Go toolchain patch version,
which `go.mod`'s own `go` directive decides rather than anything in CI (by
default Go downloads and uses whatever version `go.mod` asks for, whatever
is installed, so `GOTOOLCHAIN=local` in the `build-deb` job turns a
mismatch into a build failure instead of a silent substitution), and the
same `debhelper`/`dpkg` versions doing the packaging. A different Go patch
release is not verified to produce the same binary. It has not been checked
across different machines, architectures or Debian/Ubuntu releases;
"reproducible" here means "the same commit, rebuilt on hosts with matching
toolchain and packaging tool versions, matches", not "reproducible by
anyone, on anything, forever."

## Software bills of materials

CI generates a CycloneDX 1.6 SBOM for every compiled artefact, uploads
them as build artifacts, and (on a push to `main`) records them as
signed attestations against the artefact they describe, alongside the
build-provenance attestation that was already there. `gh attestation
verify` will show both.

| Artefact | SBOM | Where the list comes from |
|---|---|---|
| `synctang-unlocker_*.deb` | `unlocker.cdx.json` | Go build info in the shipped binary |
| `synctang-keyholder_*.deb` | `keyholder.cdx.json` | Go build info in the shipped binary |
| the APK | `app-go.cdx.json` | Go build info in the `libgojni.so` gomobile built |
| the APK | `app-jvm.cdx.json` | Gradle's resolved runtime classpath |

`scripts/generate-sbom.sh <artefact> <output.cdx.json>` does the Go
side and runs locally too; it accepts a `.deb`, an `.apk`, or a plain
executable. The APK needs two documents because its dependencies come
from two ecosystems and no single tool sees both: syft pointed at an
APK reports exactly one component, the APK itself, since nothing can
recover Maven coordinates from dex bytecode. Keeping them as two
documents rather than merging them is deliberate, so it stays visible
which evidence came from where.

Everything is read out of the built artefact rather than from `go.mod`
or `build.gradle.kts`, so an SBOM describes what was actually linked
rather than what was declared. That distinction is not academic here:
`THIRD_PARTY.md` records that `pion/ice` and `pion/stun` really are
linked into every Go binary, arriving with `syncthing-socket`'s root
package, even though no ICE code path is ever reached. A
manifest-derived SBOM would disagree with that entry.

The dracut package has no SBOM: it ships shell scripts and a systemd
unit, with nothing compiled in it.

## Licence

AGPL-3.0-or-later. See `LICENSE`.
