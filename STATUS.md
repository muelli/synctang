# Status

Current state only, kept up to date in place; not a session log. History
lives in `git log`. Conventions and hard-won gotchas live in `AGENTS.md`,
not here. See `TESTREPORT.md` for the acceptance table (A1-A11).

## Work packages

- **WP0** (curve/feasibility spike): done at emulator level. Real
  StrongBox + biometric retest still needs a human with a suitable
  Android phone (see `docs/wp0-keystore-ecdh.md`, "For the human"):
  the one genuine external blocker left in this project.
- **WP1** (`mrcore` math): done.
- **WP2** (transport): done. `SyncthingRelay` (public relay + global
  discovery), `LocalDiscovery` (LAN multicast, no Internet needed),
  and `Multi` (races the two). Battle-tested against a real
  deployment; see `AGENTS.md` for what that surfaced.
- **WP3** (`unlocker`): `enrol` (add or `--remove` a recipient) and
  `agent` done. `status` and `pair` not started.
- **WP4** (`keyholder`): `init`/`unlock`/`export-pubkey` (file backend)
  done. `pair` and the TPM2 backend not started.
- **WP5** (Android app): built (`gomobile bind` + Kotlin UI), passes
  its own instrumented tests on an emulator, CI builds and tests it.
  Real-device retest is WP0's blocker above, shared.
- **WP6** (dracut module `90unlocker`): done. `enrol` auto-rebuilding
  the initrd is not wired in; currently a manual `dracut --force` step.
- **WP7** (VM acceptance): see `TESTREPORT.md`. A1, A2, A3, A5, A8, A9
  verified on the real VM. A4 blocked on real hardware (WP0). A6
  unit-tested only, not end-to-end. A7 and A10 unblocked (A3 passes)
  but not yet run. A11 (offline/LAN-only, beyond the original plan):
  the transport itself confirmed against the real VM; a full recovery
  specifically carried over it, rather than the relay, is not yet the
  one that happened to win a real-VM race.

## CI

Green: `go-test`, `go-test-privileged`, `lint`, `em-dash-check`,
`android` (the last one for the first time as of `59614d8`; see
`AGENTS.md` for the four issues chasing that down turned up).

## Open items

- `[U]` Whether `go-tpm`'s ECDH support is reachable for the
  `keyholder` TPM2 backend; may remain a stub with tests skipped.
- `[U]` Three fixes for `syncthing-socket` (bare `go.mod` module path,
  `ensureResolver` accepting a loopback-only stub, `RunClient` not
  retrying a stale discovery record) were reported to the user as a
  written summary; not yet turned into patches or a PR, pending their
  say.
- `[U]` A `mrcore`/transport code-review pass found several real
  simplifications beyond the bugs already fixed (a `Token`
  Marshal/Unmarshal refactor, `Group.Negate` via `ScalarMult(N-1)`,
  `slices.Contains` instead of a hand-rolled equivalent, caching the
  discovery HTTP client); not applied, undecided whether before or
  after the rest of WP7 closes out.
- `[I]` `LocalDiscovery` binds to whichever network interface the
  kernel picks by default; a host with more than one active interface
  might announce on only one of them. Not fixed; `SyncthingRelay`
  remains the fallback for that case.
