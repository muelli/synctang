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
| A3 | Reboot, unlock from laptop `keyholder unlock` | `[U]` in progress. First reboot (2026-09-18) surfaced a real bug: the agent never got a working DNS resolver in the initrd (looped indefinitely trying to reach `relays.syncthing.net` against a refusing loopback stub resolver), so it never announced itself to discovery; fixed. Second issue found: the wrong discovery announce URL, and the relay dial not retrying a failed lookup/join; both fixed. Third issue found (2026-09-19): the relay dial's retry still did not cover a failed TLS handshake after a successful join; fixed in commit `6861f0d`. Root cause of that handshake failure found by reading `strelaysrv`'s own source: the relay server drops every session belonging to a device the instant that device's control connection disconnects, and `listenRelay` was disconnecting its own control connection immediately after joining a session, before the handshake on it had even started. Fixed by keeping the control connection alive until the resulting `Conn` is closed, not merely until it was joined. `[U]` fix not yet retested against the real VM as of this line (pending redeploy and reboot) | console log described by a human with hypervisor access; `keyholder unlock` output, this session |
| A4 | Reboot, unlock from phone | `[U]` pending, blocked on a real StrongBox-capable phone; Android app built and passes its own instrumented tests against the fixed MR-1 vectors (WP5) | |
| A5 | Console prompt fallback with key holder absent | `[V]` 2026-09-18: with the agent stuck (pre-fix), a human typed the LUKS passphrase directly at the console's systemd-cryptsetup prompt and the boot proceeded normally, confirming the ordinary console prompt keeps working alongside the agent, exactly as designed | human-described console session, this conversation |
| A6 | Unpaired key holder rejected | `[V]` at the transport level, unit-tested (T2.2, `mrcore/transport/syncthing_relay_test.go`); `[U]` end to end on the VM | |
| A7 | Confirm-code mode | `[U]` pending, blocked on A3 passing first | |
| A8 | Revoke one recipient, the other still unlocks | `[V]` `unlocker enrol --remove <recipient-transport-id>` implemented (`cmd/unlocker/enrol.go`'s `removeRecipient`): destroys the recipient's own LUKS2 keyslot (`cryptsetup luksKillSlot`) and removes its entry from the token, leaving the other recipient's keyslot and token entry untouched. `TestRemoveRecipientRevokesOnlyThatOne` proves the removed recipient's recovered secret no longer opens the device while the surviving recipient's still does (the actual A8 property, not just token bookkeeping); `TestRunEnrolRemoveEndToEnd` covers it at the CLI level. `[U]` end to end on the VM | `go test ./cmd/unlocker/...`, this session |
| A9 | Stop hook leaves no agent after pivot | `[V]` 2026-09-18: after the console-unblocked boot completed, no `unlocker` process and no `/run/unlocker-luks.pid` remained; the journal shows only the start line, confirming the pre-pivot hook killed it as designed | `ps aux`, `ls /run/unlocker-luks.pid` (absent), `journalctl -b` on the VM |
| A10 | Timings, network-up to unlock, 3 runs per key holder type | `[U]` pending, blocked on A3 passing first | |
| A11 | Unlock with no Internet route, same LAN only (beyond the plan's original A1-A10, added this session) | `[V]` offline in this environment: `transport.LocalDiscovery` (IPv4 multicast rendezvous) plus `transport.Multi` (races it against `SyncthingRelay`), wired into both `unlocker agent` and `keyholder unlock` by default. `TestAgentRecoversOverLocalDiscovery` runs the full enrol-listen-dial-recover-answer sequence over multicast alone, no relay or discovery server involved, skipping cleanly if the environment restricts multicast. `[U]` not yet verified on the real VM against a genuinely offline second host; the current single test VM does not offer a second machine on the same LAN | `go test ./mrcore/transport/... ./cmd/unlocker/...`, this session |

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
