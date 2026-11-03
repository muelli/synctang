<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Following the app with adb

A debug build of the app logs what it is doing, so that it can be followed from a
computer instead of by watching the screen. A release build logs warnings only.

## The command

    adb logcat -v time -s synctang GoLog

`synctang` is the app's own log (the Kotlin side: every screen change, every tap, the
fingerprint prompt, the key operations with their timings, failures with their whole
cause chain). `GoLog` is the bridge to the transport written in Go (every stage a connect
or a pairing passes through, with the attempt number and how long it has taken, which route
got through, and each failed attempt with its error). On Android the Go lines arrive at
priority I, since the bridge writes through the standard library's log writer, which
gomobile points at logcat; writing raw to stderr would have made every line an error.

`adb logcat -c` first clears what is already there. Add `-d` to dump and exit.

## What is never in it

The pairing code and the pairing link, the passphrase, scalars, wrapped or encrypted
blobs, public keys in full, and the identity seed are not logged, at any level; tests fail
if one appears (`AppLogTest`, `UnlockFlowTest`, `mobile/log_test.go`). Device IDs, machine
names, discovery URLs and addresses are logged, and a key identifier only as its first
eight characters.

## What a normal unlock looks like

Illustrative: built from the names the app uses, not pasted from a run, so the times and
IDs are made up and the exact wording may differ a little.

    synctang: action connect machine=study id=ABCDEFG-...-XYZ1234
    synctang: state Connecting machine=study id=ABCDEFG-... stage=none attempt=0 route=unknown
    GoLog: level=DEBUG msg="connect starting" machine=ABCDEFG-... timeout_s=90
    GoLog: level=TRACE msg=stage who=session from=none to=starting attempt=0 elapsed_ms=0
    GoLog: level=TRACE msg=stage who=session from=starting to=looking_up attempt=1 elapsed_ms=2
    GoLog: level=TRACE msg=stage who=session from=looking_up to=connecting attempt=1 elapsed_ms=420
    GoLog: level=TRACE msg=stage who=session from=connecting to=checking_identity attempt=1 elapsed_ms=21890
    GoLog: level=TRACE msg="route won" who=session route=relay attempt=1
    GoLog: level=TRACE msg=stage who=session from=checking_identity to=waiting_for_request attempt=1 elapsed_ms=22310
    GoLog: level=DEBUG msg="request received" kid=6f3a91c2 machine_name=study
    GoLog: level=TRACE msg=stage who=session from=waiting_for_request to=request_ready attempt=1 elapsed_ms=22340
    GoLog: level=DEBUG msg="connect returned" machine=ABCDEFG-... attempts=1 route=relay connected_ms=30
    synctang: state Approve machine=study id=ABCDEFG-... peer=ABCDEFG-... kid=6f3a91c2 expiresAt=...
    synctang: window armed 74960 ms
    synctang: action approve machine=study
    synctang: state Working machine=study id=ABCDEFG-... stage=none attempt=0 route=unknown
    synctang: prepare cipher start
    synctang: prepare cipher end 12 ms
    synctang: biometric prompt shown
    synctang: biometric prompt result SUCCEEDED
    synctang: unwrap key start
    synctang: unwrap key end 9 ms
    GoLog: level=TRACE msg=stage who=session from=request_ready to=sending_answer attempt=1 elapsed_ms=31200
    GoLog: level=DEBUG msg="answer sent"
    GoLog: level=TRACE msg=stage who=session from=sending_answer to=waiting_for_verdict attempt=1 elapsed_ms=31201
    GoLog: level=DEBUG msg=verdict ok=true reason=
    GoLog: level=TRACE msg=stage who=session from=waiting_for_verdict to=unlocked attempt=1 elapsed_ms=31950
    synctang: state Done machine=study id=ABCDEFG-...

A stall shows as the last stage reached and how long ago: a connect stuck at
`looking_up` is discovery, at `connecting` a relay, at `checking_identity` the machine's
identity, at `waiting_for_request` a machine that is slow to ask. A failed attempt shows
as `attempt failed` with its error, and the connect as `connect failed` at warn with its
attempts, which is what a release build keeps.

## Levels

A debug build turns the Go side to `trace` when the app starts (`Mobile.setLogLevel`) and
logs everything on the Kotlin side. A release build leaves the Go side at its default,
`warn`, and writes only warnings from Kotlin; the messages for the lower levels are not
even built (`AppLogTest`), and a whole Go connect logs nothing at the default level
(`TestTheDefaultLevelEmitsNoTraceLines`).
