#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Powers the emulator VM off once nobody has used it for a while, so that a
# forgotten instance stops costing money. Run every few minutes by
# emulator-idle.timer. The instance is configured to STOP, not terminate,
# when it shuts itself down, so its disk and installed tools survive.
#
# "Used" means any of:
#   * an ssh session that runs a command (sshd shows it as user@notty) exists
#     for as long as that command runs, which covers an agent or a script
#     driving the VM, however long a build takes;
#   * an interactive ssh session (user@pts/N) whose terminal had input or
#     output within IDLE_MINUTES;
#   * the keepalive file was touched within IDLE_MINUTES, for a background
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

reason=""
while IFS= read -r line; do
	case "$line" in
	"sshd: "*"@notty"* | "sshd-session: "*"@notty"*)
		reason="an ssh command is running ($line)"
		break
		;;
	"sshd: "*"@pts/"* | "sshd-session: "*"@pts/"*)
		tty=${line##*@}
		tty=${tty%% *}
		last=$($mtime_cmd "/dev/$tty" 2>/dev/null || echo 0)
		if [ $((now - last)) -lt "$limit" ]; then
			reason="interactive session on $tty was used $(((now - last) / 60)) minutes ago"
			break
		fi
		;;
	esac
done < <($ps_cmd)

if [ -z "$reason" ] && [ -e "$keepalive" ]; then
	last=$($mtime_cmd "$keepalive" 2>/dev/null || echo 0)
	if [ $((now - last)) -lt "$limit" ]; then
		reason="keepalive was touched $(((now - last) / 60)) minutes ago"
	fi
fi

if [ -n "$reason" ]; then
	echo "$now" > "$last_active_file"
	log "in use: $reason"
	exit 0
fi

last_active=$(cat "$last_active_file")
idle=$((now - last_active))
if [ "$idle" -ge "$limit" ]; then
	log "idle for $((idle / 60)) minutes, the limit is $idle_minutes: powering off"
	$poweroff_cmd
else
	log "idle for $((idle / 60)) of $idle_minutes minutes"
fi
