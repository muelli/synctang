#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Powers the emulator VM off once nobody has used it for a while, so that a
# forgotten instance stops costing money. Run every few minutes by
# emulator-idle.timer. The instance is configured to STOP, not terminate,
# when it shuts itself down, so its disk and installed tools survive.
#
# "Used" means any of, and it powers off IDLE_MINUTES after the most recent:
#   * an ssh session that runs a command (sshd shows it as user@notty) exists
#     for as long as that command runs, which covers an agent or a script
#     driving the VM, however long a build takes;
#   * an interactive ssh session (user@pts/N) whose terminal last had input or
#     output at some time (its own timestamp counts, not the time of the check);
#   * the keepalive file was last touched at some time, for a background
#     job nobody is logged in for (emu-ctl.sh keepalive).
# A bare port forward (ssh -N, the phone's adb tunnel) is deliberately not
# use: leaving one open must not keep the VM running.
#
# OpenSSH 9.8 and later (Ubuntu 26.04 has 10.2) names the per-session process
# "sshd-session" where older versions say "sshd"; both are recognised. A
# script that knew only the old name, as the first version here did, sees no
# session at all on such a system and powers it off in the middle of a build.
#
# Independently of all that, it powers off once it has been running for
# MAX_RUNTIME_HOURS, so a session that is never closed cannot run for ever.
#
# Settings come from /etc/emulator-vm.conf; everything else below can be
# overridden through the environment, which is how the tests drive it.
set -euo pipefail

conf=${EMULATOR_VM_CONF:-/etc/emulator-vm.conf}
if [ -r "$conf" ]; then
	# shellcheck source=/dev/null
	. "$conf"
fi
idle_minutes=${IDLE_MINUTES:-60}
max_hours=${MAX_RUNTIME_HOURS:-10}

state=${STATE_DIR:-/run/emulator-vm}
keepalive=${KEEPALIVE_FILE:-$state/keepalive}
now=${NOW:-$(date +%s)}
uptime_s=${UPTIME_SECONDS:-$(cut -d. -f1 /proc/uptime)}
ps_cmd=${PS_CMD:-ps -eo args=}
mtime_cmd=${MTIME_CMD:-stat -c %Y}
poweroff_cmd=${POWEROFF_CMD:-systemctl poweroff}

log() { echo "emulator-idle: $*"; }

mkdir -p "$state"
last_active_file=$state/last-active
# The clock starts at boot, not at the first run of this script, so a VM
# that is started and never used still stops idle_minutes after it booted.
[ -e "$last_active_file" ] || echo $((now - uptime_s)) > "$last_active_file"

limit=$((idle_minutes * 60))

if [ "$uptime_s" -ge $((max_hours * 3600)) ]; then
	log "running for $((uptime_s / 3600)) hours, the limit is $max_hours: powering off"
	$poweroff_cmd
	exit 0
fi

# The time of the most recent use, not whether there is any "right now".
# A running ssh command is use at this very moment; a terminal and the
# keepalive file are use as of when they were last touched. Recording that
# time, rather than the time of this check, is what makes the instance stop
# idle_minutes after the last use: recording the check's own time would make
# a keepalive count for an hour and then wait a further hour idle.
activity=0
reason=""
while IFS= read -r line; do
	case "$line" in
	"sshd: "*"@notty"* | "sshd-session: "*"@notty"*)
		activity=$now
		reason="an ssh command is running ($line)"
		break
		;;
	"sshd: "*"@pts/"* | "sshd-session: "*"@pts/"*)
		tty=${line##*@}
		tty=${tty%% *}
		last=$($mtime_cmd "/dev/$tty" 2>/dev/null || echo 0)
		if [ "$last" -gt "$activity" ]; then
			activity=$last
			reason="an interactive session on $tty, last used $(((now - last) / 60)) minutes ago"
		fi
		;;
	esac
done < <($ps_cmd)

if [ -e "$keepalive" ]; then
	last=$($mtime_cmd "$keepalive" 2>/dev/null || echo 0)
	if [ "$last" -gt "$activity" ]; then
		activity=$last
		reason="the keepalive was last touched $(((now - last) / 60)) minutes ago"
	fi
fi

last_active=$(cat "$last_active_file")
if [ "$activity" -gt "$last_active" ]; then
	last_active=$activity
	echo "$last_active" > "$last_active_file"
fi

idle=$((now - last_active))
if [ "$idle" -ge "$limit" ]; then
	log "idle for $((idle / 60)) minutes, the limit is $idle_minutes: powering off"
	$poweroff_cmd
else
	log "last use ${reason:-at boot or earlier}; idle for $((idle / 60)) of $idle_minutes minutes"
fi
