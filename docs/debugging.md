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
server: at once when that screen appears or the app comes back to the foreground, then every
60 seconds with 20 percent jitter, only while that screen is showing and the app is in the
foreground, and never two at the same time. The machine announces itself every 60 seconds and a
record is fresh for 150, so asking more often could not tell anything new. "Check again" (Ctrl+R)
and a long press on the dot look at once, but a check never starts less than 10 seconds after
the last one, nor while the server has asked to be left alone.
The app never connects to the machine to find out: it serves one session, and a probe would use
it up. A machine that is waiting (the agent in the initrd, or `synctang-unlocker open` or `pair`)
republishes its record every minute for the first ten minutes, then less and less often (every
two minutes, then four, eight and sixteen, and at most every thirty: a server that says how often
it wants to be told, with Reannounce-After, caps that), and starts again at once when its address
changes. The app calls a record at most 150 seconds old "waiting", so a machine that has been at its
prompt for more than ten minutes or so shows amber, "announced N min ago", though it still waits; that
is what the record says, and Unlock works all the same:

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

### When an unlock gives up

A dial that finds nothing for 90 seconds (the app) or `--timeout` (the command line, two minutes
by default) ends with one more lookup of the machine on its discovery server, and the headline of
the failure says what that found, so that "could not reach the machine" is not all there is to
read. A record at most 150 seconds old: "The machine announced itself N s ago but did not answer.
Try again. If it keeps failing, check its network." An older one: "The machine last announced
itself N min ago, so it may not be at its prompt. Check the machine." No record: "The machine has
not announced itself. It may still be starting, switched off, or using another discovery server."
The age is in seconds, minutes (under two hours), hours (under two days) or days. When the lookup
itself fails, or is answered with a 429, the app says what the chip last knew (grown older by the
time since, and without a lookup if the server has asked to be left alone) and the command line
adds nothing: the general text stays. The lookup is made only when the machine could not be
reached, not for a failure partway through the exchange, and never for `--direct-addr`.
`[V]` in `mrcore/transport/machine_status_test.go` (the units and the texts),
`cmd/synctang-keyholder/unlock_hint_test.go` (against a fake discovery server: two lookups, the
dial's first and this one, and none for a direct address) and `UnlockFlowTest`/`ReadinessTest`
(one lookup, only for an unreachable machine, each state, a 429).

### When the discovery server says "too many requests"

The public discovery server is shared, and a client that asks too often gets HTTP 429 with a
`Retry-After` header (measured from one host: 350 to 1200 seconds, after about a dozen requests at
two a second). That is not a network failure and says nothing about the machine, so the chip does
not call it one: it goes on showing the last known state (the grey "Checking the machine" if there
is none yet), logs `status rate limited, not asking for N s`, and nothing asks again, polls,
foreground returns or taps, until the wait is up. The wait is the header's value (seconds or an
HTTP date), at least 30 seconds and at most 10 minutes, and a minute when the header cannot be
read. The Go side calls the state `RATE_LIMITED` with `RetryAfterSeconds`.

A dial (the app's Unlock and `synctang-keyholder unlock`) looks the machine up again each time an
attempt fails, and waits 3, 5, 8, 12 and then 15 seconds between lookups, each moved by up to 20
percent, so 90 seconds is about nine lookups and not thirty. When a lookup is answered with a 429
the dial waits for the `Retry-After` instead if that is longer (at most a minute, and no longer
than the dial has left). The stage "looking up the machine" is still reported before each lookup.

`[V]` the decision against a fake discovery server pinned by Device ID with a self-signed
certificate (`mobile/status_test.go`: seconds ago, at the limit, just over it, an hour ago,
a future timestamp, no timestamp, an unreadable one, no record, a dead server, the wrong
server, a 429 with each form of Retry-After), the poller's lifecycle in `UnlockFlowTest` (asks while Ready and in the foreground,
stops on any other screen and in the background, resumes, asks at once and then not before its interval, never two at a time, a tap or a return
within 10 seconds of a check is ignored, a 429 keeps the last state and silences everything for
its wait), the dial's schedule and its lookup counts against a fake discovery server
(`mrcore/transport/discovery_backoff_test.go`: nine lookups in 90 s, a machine appearing at 5 s
found at 8 s, a Retry-After honoured and recovered from), the strings, the colours' contrast
against the dark theme (3:1 or better) and the announce-on-change rule (`ReadinessTest`).
`[U]` on a phone: how the chip looks and reads, which is compiled
(`ReadinessChipTest`) and not run.
