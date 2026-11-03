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

command -v synctang-unlocker > /dev/null 2>&1 || return 0

# rd.synctang=0 turns the whole thing off from the kernel command line, the escape
# hatch when the network path is what is broken and you just want the console
# prompt.
if ! getargbool 1 rd.synctang; then
	info "synctang-unlocker: disabled by rd.synctang=0"
	return 0
fi

# The initqueue can run a hook more than once, and one agent is enough.
[ -e /run/synctang-luks.pid ] && return 0

# Setting up a resolver is deliberately left to the agent. This hook runs when udev
# has settled, which is before the network is up, so anything decided here would be
# decided at the one moment when the answer is always "no resolver yet".

# rd.synctang.log=LEVEL sets how much the agent logs (default info); see synctang_log_level
# in the library installed beside the hooks. The agent writes to the journal itself; the
# console is what a person at the machine watches. An initrd without the library starts the
# agent at its default level rather than not at all.
# shellcheck disable=SC1091
if [ -r /usr/lib/synctang/synctang-lib.sh ] && . /usr/lib/synctang/synctang-lib.sh; then
	synctang_start_agent "$(synctang_log_level)"
else
	warn "synctang-unlocker: /usr/lib/synctang/synctang-lib.sh is missing from the initrd, so rd.synctang.log= is ignored"
	synctang-unlocker agent > /dev/console 2>&1 &
	SYNCTANG_AGENT_PID=$!
fi
echo "$SYNCTANG_AGENT_PID" > /run/synctang-luks.pid
info "synctang-unlocker: password agent started"
