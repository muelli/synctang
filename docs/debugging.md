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

## The machine side

The agent that runs in the initrd writes its log to the systemd journal, through journald's
own socket (`/run/systemd/journal/socket`, which exists in the initrd because journald runs
there), under the identifier `synctang-unlocker`. Once the disk is unlocked and the system
is up, this shows what the agent did while it waited:

    journalctl -b -t synctang-unlocker
    journalctl -b -t synctang-unlocker -o verbose     # one field per attribute, e.g. TRANSPORT_ID

The identifier is set explicitly because journald otherwise files an entry under the
process's name, which the kernel cuts to fifteen characters: `synctang-unlock`, which
`-t synctang-unlocker` does not match. Priorities follow the level: trace and debug are
7, info 6, warn 4, error 3 (`journalctl -p warning` filters on them). Each attribute of a
record is a field in upper case, and a value with newlines is sent in journald's
length-prefixed form so that it arrives whole.

Warnings and worse also go to the console, where a person at the machine watches, and if
the journal cannot be had everything goes there and that is said once. The agent is never
stopped or slowed for the journal: writes have a one-second deadline.

`rd.synctang.log=LEVEL` on the kernel command line (or at the boot prompt) sets how much it
says: `trace`, `debug`, `info` (the default), `warn` or `error`; any other value is ignored
with a message at boot. `trace` adds the transport's detail (which relay was joined, what
was announced to discovery, each lookup), which is what to turn on when a machine is not
being found. `synctang-unlocker agent --journal=auto|on|off` chooses the journal: `auto`,
the default, uses it when the socket exists. `open` and `pair` log to the console only.

`[V]` the protocol and the handler against a fake journal socket (`internal/journal`, and
`cmd/synctang-unlocker` `journal_log_test.go`: priorities, fields, multi-line values, the
deadline, the fallback said once, and that no pairing code, secret, passphrase or key
appears in what the agent logs while pairing at trace level); the shell side
(`scripts/test-agent-log.sh`). `[U]` against a real journald in a real initrd.

## The machine's status on the main screen

The dot and the line under "Paired with ..." come from one lookup on the machine's discovery
server every five seconds, only while that screen is showing and the app is in the foreground.
The app never connects to the machine to find out: it serves one session, and a probe would use
it up. A machine that is waiting (the agent in the initrd, or `synctang-unlocker open` or `pair`)
republishes its record every minute, and the app calls a record at most 150 seconds old
"waiting":

| Dot and words | Meaning |
| --- | --- |
| green, "The machine is waiting for you" | announced within 150 s (a timestamp in the future counts as now) |
| amber, "The machine announced itself N min ago. It may not be at its prompt" | a record that is older, or has no timestamp |
| red, "The machine has not announced itself. It may still be starting, or switched off" | discovery has no record |
| grey, "Cannot check: this phone cannot reach the discovery server" | the lookup itself failed, so nothing is known about the machine |
| light grey, "Checking the machine" | before the first answer |

It says what the machine published and when. It does not say that an unlock will work: a record
can be fresh and the machine's relay gone a moment later, and an old one does not mean the
machine cannot be unlocked. Colour is never the only signal (the words are always there), the
state is announced politely to a screen reader when it changes and not on every poll, and the
Unlock button is never disabled. When a dial gives up, the technical details of the failure
(not the headline) carry what the status last said, for example "Last known status of the
machine: The machine announced itself 47 min ago ...". Each check is in the log (`adb logcat -s
synctang`, "status waiting age=12") and the Go side's at trace.

`[V]` the decision against a fake discovery server pinned by Device ID with a self-signed
certificate (`mobile/status_test.go`: seconds ago, at the limit, just over it, an hour ago,
a future timestamp, no timestamp, an unreadable one, no record, a dead server, the wrong
server), the poller's lifecycle in `UnlockFlowTest` (asks while Ready and in the foreground,
stops on any other screen and in the background, resumes), the strings, the colours' contrast
against the dark theme (3:1 or better) and the announce-on-change rule (`ReadinessTest`).
`[U]` on a phone: how the chip looks and reads, which is compiled
(`ReadinessChipTest`) and not run.
