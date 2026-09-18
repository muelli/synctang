# Test report

Filled in as WP7 (VM acceptance) proceeds. `[V]` verified as described,
`[I]` inferred, `[U]` unknown, pending. See `STATUS.md` for the session
log this report is drawn from.

## Versions

| Component | Version | How checked |
|---|---|---|
| Test VM OS | Ubuntu 26.04.1 LTS | `[V]` `lsb_release -a` on `ubuntu2604-evo.fritz.box`, 2026-09-18 |
| dracut (VM) | 110-11ubuntu0.1 | `[V]` `dracut --version` on the VM, 2026-09-18 |
| cryptsetup (VM) | 2.8.4 | `[V]` `cryptsetup --version` on the VM, 2026-09-18, installed during this session (was missing) |
| cryptsetup (dev host) | 2.8.4 | `[V]` `cryptsetup --version`, same package, installed during this session |
| Go | 1.26.0 | `[V]` `go version`, dev host |
| Android SDK cmdline-tools | 11076708 | `[V]` `docs/wp0-keystore-ecdh.md` |
| Android build-tools / platform | 34.0.0 / android-34 | `[V]` `docs/wp0-keystore-ecdh.md` |
| Gradle | 8.7 | `[V]` `docs/wp0-keystore-ecdh.md` |
| Kernel (VM) | 7.0.0-31-generic | `[V]` `uname -r` on the VM, 2026-09-18 |

## Acceptance table

| ID | Description | Result | Artefact |
|---|---|---|---|
| A1 | Tests green | `[U]` pending | `just test` output not yet captured for this report |
| A2 | Enrol | `[U]` pending | |
| A3 | Reboot, unlock from laptop `keyholder unlock` | `[U]` pending, blocked on `keyholder unlock` (in progress) and the VM's real root volume | |
| A4 | Reboot, unlock from phone | `[U]` pending, blocked on the Android app (in progress) and a real StrongBox-capable phone | |
| A5 | Console prompt fallback with key holder absent | `[U]` pending | |
| A6 | Unpaired key holder rejected | `[V]` at the transport level, unit-tested (T2.2, `mrcore/transport/syncthing_relay_test.go`); `[U]` end to end on the VM | |
| A7 | Confirm-code mode | `[U]` pending, blocked on `unlocker agent --confirm-code` (in progress) | |
| A8 | Revoke one recipient, the other still unlocks | `[U]` pending, blocked on `unlocker enrol --remove` (not started) | |
| A9 | Stop hook leaves no agent after pivot | `[U]` pending | dracut module skeleton exists (`dracut/90unlocker/unlocker-stop.sh`), not exercised on the VM yet |
| A10 | Timings, network-up to unlock, 3 runs per key holder type | `[U]` pending | |

## Deviations from the plan

- Loopback LUKS2 tests (`cmd/unlocker`) do not need root or a privileged
  CI job, contrary to the plan's assumption: `luksFormat`, `luksAddKey`,
  `token import`/`export`, and `open --test-passphrase` all work
  unprivileged, since none of them map or decrypt the device. See
  `STATUS.md`, 2026-09-18 session.
- `unlocker pair` / `keyholder pair` (live QR/network pairing) deferred:
  the laptop path is complete without them
  (`keyholder export-pubkey` + `unlocker enrol --pubkey`); live pairing
  matters most for the phone, which needed the Android app to exist
  first. To be built once WP5 and WP7 are further along, or recorded here
  as a permanent simplification if it turns out not to be needed.
