#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Shell functions for the agent's start-up hook, kept apart from the hook so that
# scripts/test-agent-log.sh can load them without dracut. Installed into the initrd
# beside the hooks (see module-setup.sh) and sourced by synctang-start.sh. Written for
# bash, which dracut's hooks run under.
#
# The names the tests override: SYNCTANG_AGENT (the agent binary) and SYNCTANG_CONSOLE
# (where a person at the machine watches).

# shellcheck disable=SC2154,SC2034

# synctang_log_level prints the level the agent should log at: rd.synctang.log= if it is
# one of the five the agent knows, otherwise info. An invalid value is ignored with an
# info() message rather than stopping the agent from starting, because this is the boot
# of a machine that someone may be trying to unlock.
synctang_log_level() {
	local value
	value=$(getarg rd.synctang.log= 2> /dev/null) || value=
	case $value in
		trace | debug | info | warn | error)
			echo "$value"
			;;
		"")
			echo info
			;;
		*)
			info "synctang-unlocker: ignoring rd.synctang.log=$value (want trace, debug, info, warn or error)"
			echo info
			;;
	esac
}

# synctang_start_agent LEVEL starts the agent in the background at that log level, its
# output on the console, and leaves its PID in SYNCTANG_AGENT_PID, which the stop hook
# sends SIGTERM to. The agent writes its own log to the systemd journal, through journald's
# socket, as synctang-unlocker; what reaches the console is warnings and worse, and
# everything if the journal cannot be had (see --journal in synctang-unlocker(8)).
synctang_start_agent() {
	local level=$1
	local agent=${SYNCTANG_AGENT:-synctang-unlocker}
	local console=${SYNCTANG_CONSOLE:-/dev/console}
	"$agent" agent --log-level "$level" > "$console" 2>&1 &
	SYNCTANG_AGENT_PID=$!
}
