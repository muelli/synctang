<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Security policy

synctang decides whether a machine's disk gets unlocked, so a vulnerability in it matters.
Please report one privately, not in a public issue or pull request.

## How to report

Email Tobias Mueller, <muelli@cryptobitch.de>, with what you found, how to reproduce it,
which version or commit, and what you think an attacker gains. There is no encrypted
channel set up yet; if you need one, say so in a first message that leaves the details out.

This is a one-person project. There is no response-time promise and no bounty; reports
are read, answered and fixed as soon as they can be. Please give a reasonable time before
making a report public.

## What is in scope

The code in this repository: the MR-1 protocol (`mrcore`), the machine agent
(`synctang-unlocker`) and its dracut module, the laptop key holder (`synctang-keyholder`),
the Android app, the packaging, and the release and signing workflow.

Out of scope here: the Syncthing relay and discovery servers, which are Syncthing's, and
are used as they are (report those to the Syncthing project). The design assumes they can
read and alter the traffic through them; what the design claims in that case is in
`docs/threat-model.md`.

## What is already known

`docs/threat-model.md` says what synctang protects against and what it does not, and
`STATUS.md` lists the open findings of the security reviews so far. Among them: a copy of
the machine's disk plus one approval can recover the volume secret, because the machine's
identity is in the initrd and is not sealed in a TPM; there is no threshold of two key
holders; and the laptop key is a plain file. A report about something listed there is
still welcome if it adds to it, but it is not news.

## Which versions

Only the latest release, or the current `main`. There are no maintained older branches.
