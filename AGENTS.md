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
- **`cryptsetup luksAddKey` can read the existing passphrase and the
  new key from one stdin stream**: `--key-file=- --keyfile-size=N
  --new-keyfile=- --new-keyfile-size=M`, concatenated, read in that
  order. Keeps the generated secret off disk entirely. Also:
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
