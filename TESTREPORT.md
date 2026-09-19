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
| Test VM 2 | `synctang-test-2604`, Ubuntu 26.04 LTS, kernel 7.0.0-30-generic | `[V]` real LUKS2 root (converted in place by a separate hypervisor-access agent, not reinstalled), used for WP7's boot-time acceptance tests below, since `ubuntu2604-evo.fritz.box`'s root turned out not to be LUKS-encrypted despite the task brief's description (see `STATUS.md`) |

## Acceptance table

| ID | Description | Result | Artefact |
|---|---|---|---|
| A1 | Tests green | `[V]` `go test ./...` all green (mrcore, mrcore/transport, mobile, cmd/unlocker, cmd/keyholder), 2026-09-19 | `just test` output |
| A2 | Enrol | `[V]` `unlocker enrol --device /dev/vda4 ...` against `synctang-test-2604`'s real root LUKS device, from a laptop `keyholder`; `cryptsetup token export` shows the resulting `mr-1` token with the correct `S` and `keyslots`, 2026-09-18 | `cryptsetup token export --token-id 0 /dev/vda4` output, captured in this session |
| A3 | Reboot, unlock from laptop `keyholder unlock` | `[U]` in progress. First reboot (2026-09-18) surfaced a real bug: the agent never got a working DNS resolver in the initrd (looped indefinitely trying to reach `relays.syncthing.net` against a refusing loopback stub resolver), so it never announced itself to discovery; fixed. Second issue found: the wrong discovery announce URL, and the relay dial not retrying a failed lookup/join; both fixed. Third issue found (2026-09-19): the relay dial's retry still did not cover a failed TLS handshake after a successful join; fixed in commit `6861f0d`. Retest after that fix: dial and handshake now succeed, but the machine's `unlocker` agent closes the connection right after the handshake, before writing `Hello` (`reading hello: mrcore: reading message length: EOF`); cause not yet confirmed (unauthorized-peer rejection vs. a stale enrolment vs. a bug in the redundant peer match in `cmd/unlocker/agent.go`), diagnosis delegated to the hypervisor-access session | console log described by a human with hypervisor access; `keyholder unlock` output, this session |
| A4 | Reboot, unlock from phone | `[U]` pending, blocked on a real StrongBox-capable phone; Android app built and passes its own instrumented tests against the fixed MR-1 vectors (WP5) | |
| A5 | Console prompt fallback with key holder absent | `[V]` 2026-09-18: with the agent stuck (pre-fix), a human typed the LUKS passphrase directly at the console's systemd-cryptsetup prompt and the boot proceeded normally, confirming the ordinary console prompt keeps working alongside the agent, exactly as designed | human-described console session, this conversation |
| A6 | Unpaired key holder rejected | `[V]` at the transport level, unit-tested (T2.2, `mrcore/transport/syncthing_relay_test.go`); `[U]` end to end on the VM | |
| A7 | Confirm-code mode | `[U]` pending, blocked on A3 passing first | |
| A8 | Revoke one recipient, the other still unlocks | `[U]` pending, blocked on `unlocker enrol --remove` (not started) | |
| A9 | Stop hook leaves no agent after pivot | `[V]` 2026-09-18: after the console-unblocked boot completed, no `unlocker` process and no `/run/unlocker-luks.pid` remained; the journal shows only the start line, confirming the pre-pivot hook killed it as designed | `ps aux`, `ls /run/unlocker-luks.pid` (absent), `journalctl -b` on the VM |
| A10 | Timings, network-up to unlock, 3 runs per key holder type | `[U]` pending, blocked on A3 passing first | |

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
- The task brief's test VM (`ubuntu2604-evo.fritz.box`) turned out not to
  have a LUKS-encrypted root as described (plain ext4, empty
  `/etc/crypttab`). A second VM (`synctang-test-2604`) was provisioned and
  its root converted to LUKS2 in place (shrunk first, then
  `cryptsetup reencrypt --encrypt`, by a separate session with hypervisor
  access, not by a reinstall) specifically for WP7's boot-time acceptance
  tests. See `STATUS.md` for the full sequence.
