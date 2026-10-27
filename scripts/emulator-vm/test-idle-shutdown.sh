#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests idle-shutdown.sh with a fake clock, a fake process list and a fake
# shutdown command, so every decision it makes can be checked in a second.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

NOW=1000000 # an arbitrary fixed "now", in seconds

# Fake mtime: the file name decides how old the file looks.
cat > "$work/mtime" <<'SH'
#!/bin/bash
case "$1" in
/dev/pts/0) echo $((NOW - 600)) ;;        # used 10 minutes ago
/dev/pts/1) echo $((NOW - 7200)) ;;       # untouched for 2 hours
*keepalive-recent) echo $((NOW - 600)) ;; # touched 10 minutes ago
*keepalive-old) echo $((NOW - 7200)) ;;   # touched 2 hours ago
*) echo 0 ;;
esac
SH
cat > "$work/poweroff" <<SH
#!/bin/bash
echo called > "$work/powered-off"
SH
chmod +x "$work/mtime" "$work/poweroff"

# run <uptime seconds> <minutes since last activity> <ps output> [keepalive file name]
# Sets $out and $off (yes/no). Idle limit 60 minutes, runtime limit 10 hours.
run() {
	local uptime=$1 since=$2 ps_text=$3 keep=${4:-none}
	rm -rf "$work/state" "$work/powered-off"
	mkdir -p "$work/state"
	if [ "$since" != fresh ]; then echo $((NOW - since * 60)) > "$work/state/last-active"; fi
	printf '%s\n' "$ps_text" > "$work/ps.txt"
	[ "$keep" = none ] || touch "$work/state/$keep"
	out=$(EMULATOR_VM_CONF=/nonexistent NOW=$NOW UPTIME_SECONDS="$uptime" STATE_DIR="$work/state" \
		KEEPALIVE_FILE="$work/state/$keep" PS_CMD="cat $work/ps.txt" MTIME_CMD="$work/mtime" \
		POWEROFF_CMD="$work/poweroff" IDLE_MINUTES=60 MAX_RUNTIME_HOURS=10 \
		"$here/idle-shutdown.sh" 2>&1)
	if [ -e "$work/powered-off" ]; then off=yes; else off=no; fi
}

expect() { # expect <description> <yes|no>
	if [ "$off" = "$2" ]; then echo "ok   $1"; else echo "FAIL $1 (powered off: $off, wanted $2)"; echo "     $out"; fail=1; fi
}

quiet='sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups
sshd: ubuntu [priv]
/usr/lib/systemd/systemd --user'

run 7200 61 "$quiet"
expect "powers off after 61 idle minutes" yes

run 7200 59 "$quiet"
expect "stays on at 59 idle minutes" no

run 7200 120 "$quiet
sshd: ubuntu@notty"
expect "an ssh command that is still running keeps it on" no

run 7200 120 "$quiet
sshd: ubuntu@pts/0"
expect "a terminal used 10 minutes ago keeps it on" no

run 7200 120 "$quiet
sshd: ubuntu@pts/1"
expect "a terminal untouched for 2 hours does not" yes

run 7200 120 "$quiet
sshd: ubuntu"
expect "a bare port-forward tunnel does not keep it on" yes

run 7200 120 "$quiet" keepalive-recent
expect "a recent keepalive keeps it on" no

run 7200 120 "$quiet" keepalive-old
expect "an old keepalive does not" yes

run $((11 * 3600)) 5 "$quiet
sshd: ubuntu@notty"
expect "the runtime limit overrides an active session" yes

run $((9 * 3600)) 5 "$quiet
sshd: ubuntu@notty"
expect "below the runtime limit an active session is respected" no

run 1800 fresh "$quiet"
expect "30 minutes after boot with no first run recorded stays on" no

run 4000 fresh "$quiet"
expect "a VM never used stops 60 minutes after boot" yes

# An active session resets the idle clock.
run 7200 120 "$quiet
sshd: ubuntu@notty"
recorded=$(cat "$work/state/last-active")
if [ "$recorded" = "$NOW" ]; then echo "ok   activity resets the idle clock"; else echo "FAIL activity did not reset the idle clock ($recorded)"; fail=1; fi

exit "$fail"
