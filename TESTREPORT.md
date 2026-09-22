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
| A4 | Reboot, unlock from phone | `[V]` **passes**, 2026-09-21, on a real StrongBox phone against a machine sitting at its LUKS prompt, over the public Syncthing relay pool. The machine had been waiting 11 hours 40 minutes at that point, on a single relay connection, having been probed five times by an unenrolled stranger in between. Confirmed from both ends independently: the app reported success, and the machine's own journal recorded `recovery attempt succeeded` with `unlocker: unlocked` on its console, followed by `Reached target multi-user.target` and a login prompt. The two agreeing is the evidence, because the app now waits for the machine's verdict before claiming anything.<br><br>This closes WP0 as well. For the app to answer at all, Android Keystore had to perform ECDH on a StrongBox-held key, behind a biometric gate, against a peer point constructed by hand from raw coordinates; for the machine to unlock, the value returned had to be **correct**, which the LUKS2 header verified. The emulator spike could only ever show that a software Keystore accepted such a point.<br><br>Getting here took four bugs that only a person holding a phone could find, none of which any test caught: the confirm-code protocol mismatch that made the app unable to unlock any machine not in confirm-code mode (the default), the single-attempt transport, `Multi` cancelling the winning transport and so destroying the relay session it had just produced, and success being reported on sending an answer rather than on the volume opening | phone screen report, machine console and journal capture, `journalctl`-equivalent via the journal serial socket, this session |
| A5 | Console prompt fallback with key holder absent | `[V]` 2026-09-18: with the agent stuck (pre-fix), a human typed the LUKS passphrase directly at the console's systemd-cryptsetup prompt and the boot proceeded normally, confirming the ordinary console prompt keeps working alongside the agent, exactly as designed | human-described console session, this conversation |
| A6 | Unpaired key holder rejected | `[V]` at the transport level, unit-tested (T2.2, `mrcore/transport/syncthing_relay_test.go`), and `[V]` end to end on the real VM, 2026-09-19: a freshly generated, never-enrolled key holder identity (`MMFJMX5-WNLSOU3-...`) dialled the machine at its LUKS prompt over both transports and was rejected after the TLS handshake by the authorized-peers check, repeatedly (`unlocker[447]: rejected an unauthorized peer` in the machine's own journal, four times across two transports), and the machine stayed locked throughout. Worth noting for anyone reading a future failure: from the dialer's side this looks only like a timeout, since the machine closes the connection without explanation by design; the machine-side log is the evidence | `journalctl -b 0` on the VM, this session |
| A7 | Confirm-code mode | `[V]` both halves, at boot on the real VM, 2026-09-19. Needed a fix first: `--confirm-code` existed in the agent but the dracut hook invoked `unlocker agent` with no flags at all, so the mode was unreachable in the one place it matters. Added `rd.unlocker.confirm_code=1`. With it set, the console prints `confirm code: 769374` and the agent waits; a key holder answering `000000` is rejected (`unlocker: wrong confirm code from IL2O3W3-...`, logged machine-side, connection closed before any recovery request is sent) and the machine stays locked, confirmed by SSH still refusing; the same key holder answering `769374` unlocks it in about 3 seconds and the boot completes | machine console capture and `journalctl -b -1`, this session |
| A8 | Revoke one recipient, the other still unlocks | `[V]` **passes**, end to end at boot, 2026-09-19, on the local disposable VM (`scripts/make-test-vm.sh`). Two key holders, alice and bob, were enrolled on a real LUKS2 root; alice unlocked the machine at boot to establish the baseline. `unlocker enrol --remove <alice>` then destroyed alice's keyslot and token entry, leaving the header with slots 0 (the passphrase) and 2 (bob) and the token naming only bob. On the next boot alice was refused at the authorization check, machine-side, twice (`transport: peer OQ5ELTH-... is not authorized`) and the machine stayed at its prompt; bob then unlocked it in under a second and the boot completed to a login prompt. Also `[V]` by test: `TestRemoveRecipientRevokesOnlyThatOne` proves the removed recipient's recovered secret no longer opens the device while the surviving one's still does, and `TestRunEnrolRemoveEndToEnd` covers the CLI. This was the last item that could not be run on the shared VM, because it destroys a keyslot on a machine whose passphrase this side does not hold | machine console capture, `cryptsetup luksDump` and `token export` before and after, `go test ./cmd/unlocker/...`, this session |
| A9 | Stop hook leaves no agent after pivot | `[V]` 2026-09-18: after the console-unblocked boot completed, no `unlocker` process and no `/run/unlocker-luks.pid` remained; the journal shows only the start line, confirming the pre-pivot hook killed it as designed | `ps aux`, `ls /run/unlocker-luks.pid` (absent), `journalctl -b` on the VM |
| A10 | Timings, network-up to unlock, 3 runs per key holder type | `[V]` for the laptop (file-backed) key holder, three consecutive real reboots of `synctang-test-2604`, 2026-09-19, measured from the machine's own journal (`journalctl -b -o short-monotonic`) so no clock skew between hosts enters it. The key holder was started **before** each reboot and left retrying, so the interval is the protocol's and not a human's reaction time. Markers: DHCPv4 lease acquired, `unlocker: password agent started`, `recovery attempt succeeded`, `Finished systemd-cryptsetup@vda4_crypt.service`.<br><br>| run | network-up | agent up | recovery done | unlocked | **network-up to unlock** |<br>| 1 | 3.15s | 4.49s | 12.03s | 19.06s | **15.91s** |<br>| 2 | 5.37s | 7.27s | 14.99s | 22.06s | **16.70s** |<br>| 3 | 3.84s | 5.43s | 14.08s | 22.20s | **18.36s** |<br><br>Mean 16.99s, spread 15.9s to 18.4s. Most of it is not this project: 7.0s to 8.1s of every run sits between the agent having the verified secret and `systemd-cryptsetup` finishing, which is LUKS2 argon2id derivation on a header whose keyslots are configured at time cost 4 and 0.6 to 1.0 GiB, tried in order from slot 0. Measured directly on the same VM: a deliberately wrong passphrase, which forces all three keyslots to be tried, takes 6.86s. A human typing the passphrase at the console pays the same cost. The part this project actually governs, agent start to recovered and verified secret, is 7.5s, 7.7s and 8.7s, and the network-up to agent-up gap is 1.3s to 1.9s. `[U]` phone key holder not measured, blocked by A4 | `journalctl -b -o short-monotonic` on the VM for each of the three boots, `keyholder unlock` output, `cryptsetup open --test-passphrase` timing, this session |
| A11 | Unlock with no Internet route, same LAN only (beyond the plan's original A1-A10, added this session) | `[V]` **passes**, end to end against the real VM, 2026-09-19. Running it for real rather than at the transport layer is what exposed the bug that made it fail: `LocalDiscovery` announced from an unbound UDP socket, which needs a route to `239.0.0.0/8` and so in practice needs the default route, meaning a machine with no Internet could not announce at all. Local discovery was therefore working only on machines that did not need it. Confirmed to the socket level on the VM at that moment (unbound socket: `[Errno 101] Network is unreachable`; socket bound to the interface address: sends fine), then fixed by binding each announcing socket per interface. After the fix, with **both** the IPv4 and the IPv6 default route deleted and every reachability probe failing (`curl -4 https://1.1.1.1/`, `curl -6 https://discovery.syncthing.net/`, and the relay pool's own `https://relays.syncthing.net/endpoint`, all `000`), a full recovery completed in **1.2 seconds**: the agent recovered the secret, verified it against the real LUKS2 header, and the machine's journal contains zero mentions of a relay. Also `[V]` offline in the dev environment: `TestAgentRecoversOverLocalDiscovery` runs the full enrol-listen-dial-recover-answer sequence over multicast alone | agent journal and `/tmp/agent-a11c.log` on the VM, `keyholder unlock` output, this session |

## Overnight soak, 2026-09-21

Does a machine left waiting at its LUKS prompt stay reachable, and
still unlock, after hours of idling and after being probed by a
stranger? Six boots of the disposable VM, each left waiting for a
growing dwell, interrupted once mid-dwell by an unenrolled peer, then
unlocked by the mobile client.

| Dwell | Stranger probe | Unlock | Machine agreed | Relay changes |
|---|---|---|---|---|
| 10 min | reached, refused | yes, 51s | yes | 2 |
| 20 min | not reached (2m9s) | yes, 58s | yes | 3 |
| 40 min | reached, refused | yes, 19s | yes | 3 |
| 60 min | reached, refused | yes, 59s | yes | 2 |
| 90 min | not reached (1m49s) | yes, 61s | yes | 2 |
| 120 min | reached, refused | yes, 92s | yes | 2 |

Six unlocks out of six, each confirmed from both ends: the key holder
reported success *and* the machine's own console showed `unlocker:
unlocked`. Those two verdicts travel different paths, and an earlier
bug had them disagree, so neither alone is the result. Unlock times are
whole cycles including the LUKS open, not dial latency.

Two of the six stranger probes could not reach a machine that was
demonstrably healthy a minute later, both failing the same way: a relay
resetting the connection. A passive observer polling global discovery
once a minute for 21 hours alongside the run explains it. The machine
changes relay every few minutes (public relays disconnect clients, and
a `dynamic+` client walks its pool list), discovery merges
announcements rather than replacing them, and the record therefore
accumulated up to 8 relay addresses of which at most one was live at a
time. A dial that lands only on stale entries fails against a listener
that is perfectly reachable through another address in the same list.
This is why `raceRelayAddresses` tries them in parallel and why
`dialRelay` retries, and it means **a failed dial is not evidence that
a machine is down**. It also means the stranger-probe column measures
the probe's luck as much as the machine's behaviour; the machine
refused every stranger that did reach it, which is the property under
test.

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
