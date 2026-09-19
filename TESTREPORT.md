# Test report

Filled in as WP7 (VM acceptance) proceeds. `[V]` verified as described,
`[I]` inferred, `[U]` unknown, pending. See `STATUS.md` for current
overall state, and `git log` for the history this report is drawn from.

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
| Test VM 2 | `synctang-test-2604`, Ubuntu 26.04 LTS. Booted kernel 7.0.0-30-generic for most of WP7, then picked up 7.0.0-31-generic unattended partway through 2026-09-19 | `[V]` real LUKS2 root (converted in place by a separate hypervisor-access agent, not reinstalled). The kernel change matters: the -31 initrd was built by the kernel package with a dpkg PATH that excludes /usr/local/bin, so the dracut module's `require_binaries unlocker` failed, the module was silently dropped, and that initrd contained no agent at all. Fixed in the module rather than worked around; see `AGENTS.md` |

## Acceptance table

| ID | Description | Result | Artefact |
|---|---|---|---|
| A1 | Tests green | `[V]` `go test ./...` all green (mrcore, mrcore/transport, mobile, cmd/unlocker, cmd/keyholder), 2026-09-19 | `just test` output |
| A2 | Enrol | `[V]` `unlocker enrol --device /dev/vda4 ...` against `synctang-test-2604`'s real root LUKS device, from a laptop `keyholder`; `cryptsetup token export` shows the resulting `mr-1` token with the correct `S` and `keyslots`, 2026-09-18 | `cryptsetup token export --token-id 0 /dev/vda4` output, captured in this session |
| A3 | Reboot, unlock from laptop `keyholder unlock` | `[V]` **passes**, 2026-09-19, two consecutive clean reboots, fully automatic, no manual console intervention: `keyholder unlock` reports "unlocked" in 1-3 seconds, `journalctl -b 0` shows only `Finished systemd-cryptsetup@vda4_crypt.service`, no `Failed to activate ... incorrect` lines, and no leftover `unlocker` process afterward. Getting here surfaced and fixed four real bugs in sequence, each found by testing against this real VM, none of them visible from unit tests alone: (1) no working DNS resolver in the initrd; (2) the wrong discovery-announce URL, and the relay dial not retrying a failed lookup/join; (3) `listenRelay` disconnecting its relay control connection immediately after joining a session rather than keeping it alive through the handshake, which the relay server's own `dropSessions` behaviour (confirmed by reading `strelaysrv`'s source) turns into a dropped session; (4) the deepest one: `Multi` races `LocalDiscovery` against `SyncthingRelay` independently on the machine and the key holder, so the two sides could pick different transports for what was meant to be one attempt, and separately, the recovered secret itself could contain a NUL byte (roughly one enrolment in eight, being 32 uniformly random bytes) that systemd's own ask-password handling truncates at, silently delivering a wrong, short key to `cryptsetup` even though the agent's own `verifyPassphrase` (raw bytes on stdin) saw no problem at all. Fixed by hex-encoding the LUKS key material everywhere it is used, never handing systemd raw bytes | `journalctl -b 0` on the VM, `keyholder unlock` output, this session |
| A4 | Reboot, unlock from phone | `[U]` pending, blocked on a real StrongBox-capable phone; Android app built and passes its own instrumented tests against the fixed MR-1 vectors (WP5) | |
| A5 | Console prompt fallback with key holder absent | `[V]` 2026-09-18: with the agent stuck (pre-fix), a human typed the LUKS passphrase directly at the console's systemd-cryptsetup prompt and the boot proceeded normally, confirming the ordinary console prompt keeps working alongside the agent, exactly as designed | human-described console session, this conversation |
| A6 | Unpaired key holder rejected | `[V]` at the transport level, unit-tested (T2.2, `mrcore/transport/syncthing_relay_test.go`), and `[V]` end to end on the real VM, 2026-09-19: a freshly generated, never-enrolled key holder identity (`MMFJMX5-WNLSOU3-...`) dialled the machine at its LUKS prompt over both transports and was rejected after the TLS handshake by the authorized-peers check, repeatedly (`unlocker[447]: rejected an unauthorized peer` in the machine's own journal, four times across two transports), and the machine stayed locked throughout. Worth noting for anyone reading a future failure: from the dialer's side this looks only like a timeout, since the machine closes the connection without explanation by design; the machine-side log is the evidence | `journalctl -b 0` on the VM, this session |
| A7 | Confirm-code mode | `[V]` both halves, at boot on the real VM, 2026-09-19. Needed a fix first: `--confirm-code` existed in the agent but the dracut hook invoked `unlocker agent` with no flags at all, so the mode was unreachable in the one place it matters. Added `rd.unlocker.confirm_code=1`. With it set, the console prints `confirm code: 769374` and the agent waits; a key holder answering `000000` is rejected (`unlocker: wrong confirm code from IL2O3W3-...`, logged machine-side, connection closed before any recovery request is sent) and the machine stays locked, confirmed by SSH still refusing; the same key holder answering `769374` unlocks it in about 3 seconds and the boot completes | machine console capture and `journalctl -b -1`, this session |
| A8 | Revoke one recipient, the other still unlocks | `[V]` `unlocker enrol --remove <recipient-transport-id>` implemented (`cmd/unlocker/enrol.go`'s `removeRecipient`): destroys the recipient's own LUKS2 keyslot (`cryptsetup luksKillSlot`) and removes its entry from the token, leaving the other recipient's keyslot and token entry untouched. `TestRemoveRecipientRevokesOnlyThatOne` proves the removed recipient's recovered secret no longer opens the device while the surviving recipient's still does (the actual A8 property, not just token bookkeeping); `TestRunEnrolRemoveEndToEnd` covers it at the CLI level. `[U]` end to end on the VM | `go test ./cmd/unlocker/...`, this session |
| A9 | Stop hook leaves no agent after pivot | `[V]` 2026-09-18: after the console-unblocked boot completed, no `unlocker` process and no `/run/unlocker-luks.pid` remained; the journal shows only the start line, confirming the pre-pivot hook killed it as designed | `ps aux`, `ls /run/unlocker-luks.pid` (absent), `journalctl -b` on the VM |
| A10 | Timings, network-up to unlock, 3 runs per key holder type | `[I]` two of the reboots run for A3 itself give a rough number: `keyholder unlock` reported "unlocked" 1-3 seconds after being started, each run started once the console showed the agent already waiting; not yet measured properly from network-up, and only the file-backed laptop key holder, not phone | console timestamps, `keyholder unlock` output, this session |
| A11 | Unlock with no Internet route, same LAN only (beyond the plan's original A1-A10, added this session) | `[V]` at the transport layer, including against the real VM: a standalone `LocalDiscovery.Dial` probe from the dev host successfully found and authenticated `synctang-test-2604`'s real Device ID over multicast alone, no relay or discovery server involved. `[I]` both of A3's successful full `keyholder unlock` runs went via the relay path instead (console log shows "Joined relay" each time; `Multi` raced both and relay happened to win), so a *complete* recovery exchange has not yet been the one specifically carried over `LocalDiscovery` against this real VM, only the connect-and-authenticate step. Also `[V]` offline in the dev environment: `TestAgentRecoversOverLocalDiscovery` runs the full enrol-listen-dial-recover-answer sequence over multicast alone | `go test ./mrcore/transport/... ./cmd/unlocker/...`; ad hoc `LocalDiscovery.Dial` probe against the real VM, this session |

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
