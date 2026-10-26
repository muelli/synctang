#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Start the password agent, in the background, so it is already listening while
# systemd-cryptsetup waits for an answer.
#
# It must not block: this hook runs before the root filesystem is mounted, and
# anything that waits here waits forever if no key holder ever appears. The agent
# answers if it can, the console prompt stays live either way.

# shellcheck disable=SC1091
type getarg > /dev/null 2>&1 || . /lib/dracut-lib.sh

command -v unlocker > /dev/null 2>&1 || return 0

# rd.unlocker=0 turns the whole thing off from the kernel command line, the escape
# hatch when the network path is what is broken and you just want the console
# prompt.
if ! getargbool 1 rd.unlocker; then
	info "unlocker: disabled by rd.unlocker=0"
	return 0
fi

# The initqueue can run a hook more than once, and one agent is enough.
[ -e /run/unlocker-luks.pid ] && return 0

# Setting up a resolver is deliberately left to the agent. This hook runs when udev
# has settled, which is before the network is up, so anything decided here would be
# decided at the one moment when the answer is always "no resolver yet".

unlocker agent > /dev/console 2>&1 &
echo $! > /run/unlocker-luks.pid
info "unlocker: password agent started"
