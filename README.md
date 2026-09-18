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
  after boot, as a systemd password agent. Subcommands: `enrol`, `agent`,
  `status`, `pair`.
- `keyholder`: the Ubuntu laptop CLI. Subcommands: `init`, `unlock`,
  `pair`, `export-pubkey`.
- Android app (`android/`): the phone key holder. Pairs by QR code, unlocks
  behind `BiometricPrompt`, keeps its key in the Android Keystore.
- `dracut/90unlocker`: the dracut module.

## Quick start

To be filled in as each component reaches a runnable state; see `STATUS.md`
for what exists today.

## Protocol: MR-1

To be documented here once WP1 (`mrcore`) has committed test vectors in
`testdata/mr1.json`.

## Threat model

To be documented here in substance from the design plan once WP1 to WP3 are
in place; see `STATUS.md` for progress.

## Rotation and revocation

To be documented alongside `keyholder pair`/`unlocker enrol --replace` and
`unlocker enrol --remove` once WP3 and WP4 land.

## Unattended boot

Explicitly out of scope. This project trades unattended reboots for a
human-approved unlock, hardware-held key holder secrets, and a phone as a
first-class key holder. If unattended reboot is a requirement, use Tang
directly.

## Licence

AGPL-3.0-or-later. See `LICENSE`.
