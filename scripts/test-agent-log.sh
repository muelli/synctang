#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests the shell side of the agent's log in dracut/90synctang/synctang-lib.sh: which
# level the agent is started with (from rd.synctang.log=), and that it is started with
# its output on the console and its PID in hand, and leaves nothing behind when stopped.
# (The agent writes to the journal itself; that is a Go test.) The agent is a stand-in;
# no dracut.
#
# shellcheck disable=SC2329
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

check() {
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

# What dracut-lib.sh gives a hook at run time, stubbed: getarg reads the "command
# line" in $CMDLINE, info and warn write to $work/messages.
getarg() {
	local name=${1%=} word
	for word in $CMDLINE; do
		case $word in "$name"=*) echo "${word#*=}"; return 0 ;; esac
	done
	return 1
}
info() { echo "info: $*" >> "$work/messages"; }
warn() { echo "warn: $*" >> "$work/messages"; }

# shellcheck disable=SC1091
. "$here/../dracut/90synctang/synctang-lib.sh"

level_for() { CMDLINE=$1 synctang_log_level; }

check "no rd.synctang.log: info" test "$(level_for 'quiet rd.neednet=1')" = info
for level in trace debug info warn error; do
	check "rd.synctang.log=$level is passed on" test "$(level_for "rd.synctang.log=$level")" = "$level"
done
: > "$work/messages"
check "an invalid level is ignored: info" test "$(level_for 'rd.synctang.log=loud')" = info
check "an invalid level is said so with info()" grep -q '^info: .*rd.synctang.log=loud' "$work/messages"
check "an empty level is ignored: info" test "$(level_for 'rd.synctang.log=')" = info
check "the level is case sensitive: TRACE is invalid" test "$(level_for 'rd.synctang.log=TRACE')" = info

# A stand-in agent: prints two lines, then waits to be stopped, as the real one does.
marker="$work/marker-$$"
cat > "$work/agent" << EOA
#!/bin/bash
# $marker
printf '%s\n' "\$*" > "$work/agent.args"
echo "agent line one"
echo "agent line two" >&2
while :; do sleep 0.1; done
EOA
chmod +x "$work/agent"

export SYNCTANG_AGENT="$work/agent" SYNCTANG_CONSOLE="$work/console"
: > "$work/console"

# Waits (no fixed sleep) until a condition holds, for at most ten seconds.
wait_for() {
	local _
	for _ in $(seq 1 200); do
		"$@" && return 0
		sleep 0.05
	done
	return 1
}
processes_left() { pgrep -f "$work/" > /dev/null; }
no_processes_left() { ! processes_left; }
lines_arrived() { [ "$(grep -c 'agent line' "$work/console" 2> /dev/null || true)" = 2 ]; }

synctang_start_agent warn
agent_pid=$SYNCTANG_AGENT_PID
check "both of the agent's output lines reach the console" wait_for lines_arrived
check "\$! is the agent" bash -c "tr '\\0' ' ' < /proc/$agent_pid/cmdline | grep -q '$work/agent'"
check "the agent was given the level" grep -q -- 'agent --log-level warn' "$work/agent.args"
kill -TERM "$agent_pid"
check "stopping the agent leaves no process behind" wait_for no_processes_left

exit "$fail"
