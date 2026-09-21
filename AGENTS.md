# Working in this repo

## Conventions

- Licence: AGPL-3.0-or-later. SPDX header (`// SPDX-License-Identifier:
  AGPL-3.0-or-later`) on every source file. Every dependency tracked in
  `THIRD_PARTY.md`.
- No em-dashes anywhere (`scripts/check-em-dash.sh`, run in CI); British
  spelling, serial commas. Note the checker greps via `git grep`, so it
  only sees tracked or staged files: a brand new untracked file passes
  it vacuously. `git add` new files before trusting a clean run.
- No AI attribution in commits or code. Git author is always
  `Tobias Mueller <muelli@cryptobitch.de>` (set per-commit with
  `-c user.name= -c user.email=`, not the host's global git config).
- `[V]`/`[I]`/`[U]` markers in docs: verified, inferred, unknown/pending.
- Strict TDD: a failing test before the implementation that makes it
  pass. Small commits, each referencing the work package (WP0-WP7) or
  acceptance item (A1-A11) it serves.
- Never write a LUKS passphrase, ephemeral scalar, or derived key to
  disk; wipe (`wipe()`) every secret byte slice after use, including in
  error paths.
- `STATUS.md` is current-state only, not a running log; update it in
  place rather than appending another session's worth of narrative.
  History lives in `git log`.
- **The git index is shared state. Never commit while a subagent is
  working in the same tree.** A subagent that has run `git add` (even
  without committing, which is what it was told to do) leaves its work
  staged, and the next `git add <one file> && git commit` from anyone
  else sweeps all of it into that commit, under the wrong message.
  That happened here: the entire Debian packaging landed inside a
  commit titled "TESTREPORT: A6 and A7 verified end to end", and was
  pushed before anyone noticed. Either give parallel agents worktree
  isolation, or tell them not to touch `git` at all and check
  `git status` immediately before every commit.

## Hard-won gotchas (each cost real debugging time; do not rediscover)

- **`syncthing-socket`'s `go.mod` declares a bare module path**
  (`module syncthing-socket`, not the full import path). A plain
  `go get github.com/muelli/syncthing-socket` fails with a
  module-path-mismatch error; needs the `replace` directive already in
  this repo's `go.mod`. (Proposed fixing this upstream; not yet done.)
- **Go's `encoding/json` case-folds field names that differ only in
  case** onto the same struct field, and the later key in document
  order silently wins. `testdata/mr1.json` uses `s_scalar`/`c_scalar`/
  `e_scalar` rather than `s`/`c`/`e` alongside `S`/`C`/`E` for exactly
  this reason: the first version had a 32-byte scalar silently
  overwritten by a 65-byte point during decode.
- **LUKS2 requires a top-level `"keyslots"` array on every token**,
  even empty; `cryptsetup token import` rejects one without it with a
  bare "Failed to import token from file.", no further detail.
- **Give cryptsetup each secret on its own file descriptor**, via
  `exec.Cmd.ExtraFiles` and `--key-file=/proc/self/fd/3`: it then
  reads each to EOF and no length has to be named. Reading two
  secrets from one stdin stream works, but needs `--keyfile-size=N`,
  which puts the length of the operator's LUKS passphrase into argv
  where any local process can read it from `/proc/<pid>/cmdline`.
  Two things to know if you test this by hand: `sudo` closes
  inherited descriptors above 2, so a `sudo cryptsetup` in the middle
  makes `/proc/self/fd/3` point at something else entirely and it
  fails with "Maximum keyfile size exceeded"; and the pipes must be
  written from goroutines after `Start`, or a secret larger than the
  pipe buffer deadlocks against a child that has not started reading.
  Also:
  `token import` rejects a token naming a keyslot ID that does not yet
  exist, so add the keyslot before writing a token referencing it.
- **A raw random secret cannot go through systemd's ask-password
  protocol safely.** Something downstream of the ask-password socket,
  in systemd's own password handling, truncates the answer at the
  first embedded NUL byte, silently delivering a short, wrong key,
  even though `cryptsetup` itself handles the same raw bytes fine via
  a direct keyfile. A uniformly random 32-byte secret hits this
  roughly one enrolment in eight. Fix: hex-encode the secret
  (`luksKeyMaterial` in `cmd/unlocker/cryptsetup.go`) and use *that* as
  the actual LUKS2 keyslot content and the delivered answer, both at
  enrol time and at recovery time, never the raw bytes. Reproduced
  standalone with nothing but `cryptsetup`/`systemd-cryptsetup`, no
  project code involved; worth a closer look at
  `src/shared/ask-password-api.c` if this ever gets filed upstream.
- **The Syncthing relay server drops every session belonging to a
  device the instant that device's control connection to the relay
  disconnects** (`strelaysrv`'s `dropSessions`, "to realize the client
  is no longer there faster"). Do not tear down a relay client's
  control connection until the connection it produced is actually done
  being used (i.e. wire the teardown into that `Conn`'s `Close()`, not
  a `defer` right after joining).
- **The Syncthing relay client never closes its invitations channel**,
  not even when its `Serve` has returned "could not find a connectable
  relay". So `case inv, ok := <-rc.Invitations()` can never see
  `ok == false`, and anything waiting on that channel alone waits for
  ever. Its dynamic client also sets its URI to nil whenever it is
  between relays, and our announce closure returns no addresses in
  that state, which `announceOnce` treats as nothing to do and
  reports as success. Together those two produce the nastiest failure
  this project has had: a machine at its LUKS prompt loses its relay,
  gives up on the whole pool, then sits there printing "waiting for a
  key holder" while announcing nothing and listening to nobody, for
  ever, with not one line of log to say so. Its discovery record goes
  stale and the relay answers "not found" for it. `watchRelayLoss`
  exists solely to notice this and fail the attempt so the agent's
  outer loop can build a fresh client; do not remove it on the
  assumption that the channel close will do the job.
- **Never cancel the context a winning transport was built on.**
  `SyncthingRelay` builds its relay client, its control connection and
  its announce loop on the context it is handed, and the relay server
  drops every session belonging to a device the instant that device's
  control connection disconnects. So cancelling the winner does not
  tidy up a finished attempt, it destroys the session that attempt
  just produced, and both ends read EOF on a connection that finished
  its handshake a moment earlier. `Multi.race` did exactly this and
  made relay unlocks fail nearly every time. Each attempt now gets its
  own context; the winner's is released by its `Conn.Close()`. Note
  how invisible this was: `LocalDiscovery` has no control connection
  to lose and a `DirectAddr` listener has no relay at all, so all 105
  Go tests and every offline acceptance test passed throughout. Only
  the public relay pool shows it.
- **Forward the journal to a second serial port when debugging a
  machine that has not unlocked yet.** `journalctl` is useless there:
  reading it needs the root filesystem that is not mounting, so an
  agent can fail every ten seconds for hours and say nothing a human
  can see. `systemd.journald.forward_to_console=1
  systemd.journald.tty_path=/dev/ttyS1` on the kernel command line,
  plus a second `-serial` in QEMU, gives the full journal on its own
  socket while leaving the console readable. `scripts/run-test-vm.sh`
  does this on port 7101. The agent also tees warnings to the console
  for real deployments, where nobody has attached a second serial
  port.
- **Local discovery announcements are unauthenticated UDP from
  anybody on the network**, and that is fine for secrecy (the TLS
  handshake afterwards pins the Device ID, so an impostor cannot
  impersonate the machine) but not automatically fine for
  availability. A neighbour announcing the machine's own Device ID
  pointing at a dead port used to switch local discovery off
  altogether, because `Dial` took the first matching announcement and
  gave up if it led nowhere. It now keeps listening, and bounds each
  announced address separately: `dialTCPRetrying` retries until its
  context is done, which is right for an address the machine
  announced itself and wrong for one a stranger sent. Note the address
  comes from the datagram's source IP, never from the payload, so an
  attacker can only point you at a port on their own machine.
- **`transport.Multi` (races `LocalDiscovery` against
  `SyncthingRelay`) can pick a different winning transport on each
  side independently**, since each side races on its own with no
  coordination: a connection that completes its TLS handshake and then
  reads EOF, because nobody on the other end is writing to it. Two
  parts to living with this: `Multi.race` must close a second,
  late-arriving success rather than leak it (a real, authenticated
  connection nobody asked for), and the caller (`keyholder unlock`)
  must retry the whole attempt on failure rather than give up after
  one, since a fresh attempt is a fresh independent race that
  converges.
- **GitHub Actions' `${{ env.X }}` and a shell script's `$X` in the
  same workflow file are not the same thing.** `${{ }}` only resolves
  variables set via a workflow/job/step `env:` key; it cannot see an
  OS-level environment variable baked into the runner image (like
  `ANDROID_SDK_ROOT` on `ubuntu-latest`), and silently evaluates to
  empty rather than erroring. Use real shell expansion inside `run:`
  for anything the runner itself sets.
- **`ubuntu-latest` already ships a full Android SDK** (`ANDROID_HOME`,
  `ANDROID_SDK_ROOT`, `sdkmanager`, several build-tools versions;
  documented in `actions/runner-images`), but does not guarantee
  `sdkmanager` is on `PATH`, and `sdkmanager --install` for anything
  not already cached blocks on an interactive license prompt in CI
  (pipe `yes` into it).
- **GitHub's public job page HTML
  (`https://github.com/{owner}/{repo}/actions/runs/{run}/job/{job}`)
  server-renders real annotations unauthenticated** (deprecation
  warnings, the generic "Process completed with exit code N"), useful
  when there is no token for the log-download API, but never the
  actual log text a build tool printed.
- **`act` (`go install github.com/nektos/act@latest`) runs a workflow
  job locally against Docker** and is available in this environment;
  try it before spending several real-CI round trips chasing a CI
  failure blind (`act push -j <job> -P
  ubuntu-latest=catthehacker/ubuntu:act-latest`). Confirmed it correctly
  parses this repo's `ci.yml` and reproduces failures. Caveat, also
  confirmed directly: its default runner image does not carry
  GitHub-hosted runners' large pre-installed toolchains
  (`ubuntu-latest`'s real pre-installed Android SDK: `ANDROID_HOME`
  came back empty under `act`, where the real runner has one), so a
  job that depends on those needs `--env` overrides to approximate
  them, or a real CI run for that part specifically; treat `act` as a
  fast first-pass filter for workflow logic, not a full replacement for
  a real runner.
- **Sending to a multicast group from an unbound UDP socket needs a
  route to that group, which in practice means the default route.**
  Nothing on an ordinary host covers `239.0.0.0/8`, so on a machine
  with no default route the write fails outright with "network is
  unreachable". This bit `LocalDiscovery` exactly backwards: a machine
  with no default route is a machine with no Internet, which is the
  one case local discovery exists for, so it worked only where it was
  not needed. Bind each sending socket to a specific interface address
  (`net.ListenUDP` with a real `IP`, then `WriteToUDP`); the kernel
  then sends out that interface with no route lookup to fail. When
  testing "no Internet", delete the default route of **both** address
  families: a dead-but-present IPv6 default route still counts as a
  route, and makes the evidence for a LAN-only claim circumstantial.
- **`ssh -o ConnectTimeout=N` does not bound an established session.**
  Polling a rebooting machine with `ssh host true` to detect when it
  goes down will eventually catch it mid-shutdown: the TCP connection
  is already up, the peer then vanishes without a FIN, and ssh hangs
  indefinitely with no timeout in play. It wedged an unattended reboot
  harness here for fifteen minutes after the work itself had finished.
  Add `-o ServerAliveInterval=3 -o ServerAliveCountMax=2`, or wrap the
  whole call in `timeout`.
- **Android's touch mode makes the whole focus system inert**, on
  purpose: a touchscreen with no keyboard has no focus and no focus
  ring. A device enters it the moment anything taps the screen and
  leaves it when a hardware key arrives. Every keyboard-navigation
  assertion is therefore false in touch mode and true out of it, so an
  instrumented test that does not pin it passes or fails depending on
  what happened to the emulator earlier (the first green run of
  `KeyboardNavigationTest` was green only because an `adb shell input
  keyevent` had left that emulator out of touch mode). Call
  `InstrumentationRegistry.getInstrumentation().setInTouchMode(false)`
  in `@Before`. The same thing means a focus ring genuinely should not
  appear on a phone being used by touch; test it with a keyboard.
- **A `FocusRequester` is not usable in the `LaunchedEffect` that
  first runs beside it.** The modifier node it belongs to attaches
  during layout, after the effect is dispatched, so `requestFocus()`
  throws "FocusRequester is not initialized". Swallowing that, which
  is tempting since there is nothing useful to do with it, produces a
  screen that simply arrives with nothing focused and looks exactly
  like a feature that was never implemented. Wait a frame
  (`withFrameNanos`) and retry a few times.
- **A focus ring drawn in the theme's primary colour is invisible on a
  filled Material button**, which is also the primary colour. Draw it
  at the outer edge with the control inset inside it so it lands on
  the background, and reserve the inset whether or not the ring is
  showing, or focus shifts the layout.
- **A dracut initrd's own DHCP lease is a different address than the
  fully-booted OS gets** on its own network restart post-pivot. The
  serial console and SSH are never reachable at the same address
  mid-boot; do not assume otherwise when debugging a stuck boot.
- **A telnet/serial console only reliably delivers typed input to one
  connected client at a time**; a second, independent connection may
  receive output or nothing, inconsistently, but does not reliably get
  to type. Use exactly one connection for both reading and writing;
  kill any stray earlier one first. Also: a shell double-quoted
  `"foo\n"` has no real newline in it (bash does not interpret `\n`
  inside `"..."`) and will be typed as the literal characters
  `foo\n` into whatever is reading raw keystrokes, e.g. consuming a
  real password attempt at a LUKS prompt. Use `$'...'` or an actual
  `\n`/`\r` byte.
- **A `dynamic+` relay client walks the pool list once and then gives
  up for good.** `lib/relay/client/dynamic.go`'s `serve` fetches the
  pool over HTTP, orders it by latency, and then loops over that list
  running one static client per relay until it disconnects, moving to
  the next each time; when the list runs out it returns
  `errors.New("could not find a connectable relay")` and never
  re-fetches the pool. Public relays disconnect clients every few
  minutes, so a long-lived listener burns one list entry per
  disconnect. This is why `Multi` retrying a failed Listen leg
  (`TestMultiKeepsRacingAfterALegFails`) is load-bearing rather than
  belt-and-braces: the retry builds a *new* relay client, which is the
  only thing that fetches the pool again. A machine waiting at its LUKS
  prompt without that retry would eventually go off the air
  permanently, having started out perfectly healthy.
- **Global discovery merges announcements instead of replacing them**,
  so a listener's record accumulates every relay it has ever been on
  while the records live. Measured during the overnight soak of
  2026-09-21: after roughly 35 minutes across three boots of the same
  device ID, the record held 6 relay addresses, of which at most 1 was
  live. Diallers must therefore treat a failed address as normal and
  race the whole list (`raceRelayAddresses`) rather than trusting any
  single entry, and a dial failure is not evidence that the listener is
  down.

## Decisions already made (do not re-litigate without new information)

- Curve: NIST P-256 (Android Keystore and TPM2 ECDH support it;
  Curve25519 support does not exist on either).
- AEAD: AES-256-GCM, not ChaCha20-Poly1305 (both fine for this threat
  model; AES-GCM needs no new dependency, already in `crypto/cipher`).
- One Go module (`github.com/muelli/synctang`) for `mrcore`,
  `transport`, `cmd/unlocker`, `cmd/keyholder`, not one per component.
- `keyholder`'s long-term MR-1 scalar and its transport identity are
  two separate files (raw scalar vs. TLS keypair, different protocols,
  independently rotatable), never combined.
- The MR-1 wire messages live in `mrcore`, not duplicated in
  `cmd/unlocker`/`cmd/keyholder`: exactly one definition both binaries
  must agree on.
