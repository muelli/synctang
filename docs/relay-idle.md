<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# An idle session through a relay

While a person decides whether to approve an unlock, the machine and the phone
have a connection and nothing is sent on it. This note records what was measured
about how long such a connection survives, and what the app does about it.
`[V]` marks what was run here, `[U]` what is unknown.

## Measured

`integration/relay_idle_test.go` (`TestRelayIdleSessionClose`, opt-in with
`SYNCTANG_RELAY_IDLE=short|long|all`, needs the binaries as the other integration
tests do) meets through a private relay with the real `strelaysrv` and `stdiscosrv`
v1.30.0, sends one hello from the machine, then sends nothing on either side and
records when each side's read ends and how. One run, on one host, loopback-adjacent
(the host's LAN address):

| Relay flags | Both sides saw EOF after the last traffic |
| --- | --- |
| defaults (`-network-timeout 2m`, `-ping-interval 1m`) | 2 min 0 s `[V]` |
| `-ping-interval 10s`, otherwise defaults | 2 min 0 s `[V]` |
| `-network-timeout 20s` | 17.2 s in one run, 7.2 s in another `[V]`, not explained |

So the relay ends an idle session at its network timeout, both ends together,
with a clean EOF, and the ping interval does not matter `[V]`.

## Not explained

On the real phone, three sessions ended with the machine logging
`reading recover response: mrcore: reading message length: EOF` about 90 to 100
seconds after connecting, with nobody answering. That is shorter than what the
relay did here (120 s), so the relay's own timeout is not what ended those `[V]`,
and the machine's own limit (`exchangeTimeout`, three minutes) is not either. What
did is `[U]`: the relay those sessions went through may have been configured
differently or been a different version, or a network between the phone and the
relay (a mobile carrier's NAT, say) may drop an idle TCP connection after about a
minute and a half. Nothing here distinguishes them.

## What the app does

The app stops waiting, tells the machine no and says so, after
`APPROVE_WINDOW_MILLIS` (75 s), counted from when the connection to the machine was
made and not from when the Approve screen appeared (`Session.ConnectedForMillis`
reports how long ago that was, since the machine may have been slow to send its
request). 75 s is the shortest of the field sightings, 90 s, less a 15 s margin.
The window stays running through the fingerprint prompt: if it ends while the
prompt is up, the prompt is cancelled and the machine is told no. A cancelled prompt
does not end the request: the Approve screen comes back with the window still
running. That screen shows the time left, in steps of five seconds, as plain text, and
warns once, politely, at 15 s left. Unlock itself goes straight to the prompt when the
request arrives, so the Approve screen is only seen after a cancelled prompt.

`[V]` On the Pixel 3a XL (Fedora 44 VM as the machine, public relay pool,
2026-11-03, nobody touching the sensor): the Approve screen appeared 23 s after
Unlock and said "75 s left to answer"; Approve was tapped and the fingerprint
prompt came up; the prompt dismissed itself 71 s after the tap, which is 75 s
after the connection was made, and the screen said "The machine stopped waiting
for an answer, so nothing was sent". So the real `BiometricPrompt` does go away
when its coroutine is cancelled.

## A keepalive would address both causes, and was not built

Any traffic on the session resets a relay's idle timer and keeps a NAT mapping
alive, so the phone sending something every 30 seconds while it waits would cover
the relay's 2 minutes and a network's 90 seconds alike. It needs a protocol change,
which was not made: a new message type that the phone sends while it waits and that
the machine's `runExchange` reads and discards before the `RecoverResponse` it is
waiting for (today it reads the next message as the response, so a machine that does
not know the new message would fail the exchange), and a way for the phone to tell
whether the machine understands it (a field in the machine's hello, or a version
bump). With it the Approve window could be the machine's own three minutes. This
note stops at the description.
