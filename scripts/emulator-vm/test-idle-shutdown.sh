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
/dev/pts/7) echo "$KEEP_MTIME" ;;         # used at an absolute time
*keepalive-recent) echo $((NOW - 600)) ;; # touched 10 minutes ago
*keepalive-old) echo $((NOW - 7200)) ;;   # touched 2 hours ago
*keepalive-abs) echo "$KEEP_MTIME" ;;     # touched at an absolute time
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

# OpenSSH 9.8 and later (Ubuntu 26.04 has 10.2) names the per-session process
# "sshd-session", not "sshd". Seen on a real instance: a script that only knew
# the old name saw no session at all and would have powered the VM off in the
# middle of a build.
real_quiet='sshd: /usr/sbin/sshd -D -o AuthorizedKeysCommand /usr/share/ec2-instance-connect/eic_run_authorized_keys %u %f -o AuthorizedKeysCommandUser ec2-instance-connect [listener] 0 of 10-100 startups
sshd-session: ubuntu [priv]'

run 7200 120 "$real_quiet
sshd-session: ubuntu@notty"
expect "sshd-session: a running ssh command keeps it on" no

run 7200 120 "$real_quiet
sshd-session: ubuntu@pts/0"
expect "sshd-session: a terminal used 10 minutes ago keeps it on" no

run 7200 120 "$real_quiet
sshd-session: ubuntu@pts/1"
expect "sshd-session: a terminal untouched for 2 hours does not" yes

run 7200 120 "$real_quiet
sshd-session: ubuntu"
expect "sshd-session: a bare port-forward tunnel does not keep it on" yes

run 7200 120 "$real_quiet"
expect "sshd-session: only the listener and a [priv] monitor is idle" yes

# The idle clock runs from the LAST ACTIVITY, not from the last time the
# script happened to notice some. A keepalive or a terminal counts as use only
# up to the moment it was last touched; an hour after that the instance is idle
# for an hour. The bug this guards against only shows across two consecutive
# runs on the same state: the first, at 56 minutes, sees use and (in the first
# version) recorded the time of that check as the last use, so the second, at
# 61 minutes, found only five idle minutes and the instance took about two
# hours to stop instead of one. Found by thinking through what the first real
# instance's journal showed, not by the single-run tests above.
T=5000000
off=()

# seq_run <activity at T: keepalive|pts|none> <minutes after T for the first run> <... for the second>
# Runs the script twice on the same state directory. Sets off[1] and off[2].
# The instance booted two hours before T, except in the "none" case, where it
# booted at T, so that "an hour after boot" is a time the checks can straddle.
seq_run() {
	local kind=$1 m1=$2 m2=$3 ps_text="$quiet" boot=$((T - 7200))
	[ "$kind" != none ] || boot=$T
	rm -rf "${work:?}/state" "$work/powered-off"
	mkdir -p "$work/state"
	[ "$kind" != pts ] || ps_text="$quiet
sshd-session: ubuntu@pts/7"
	printf '%s\n' "$ps_text" > "$work/ps.txt"
	[ "$kind" != keepalive ] || touch "$work/state/keepalive-abs"
	local n k
	for k in 1 2; do
		if [ "$k" = 1 ]; then n=$((T + m1 * 60)); else n=$((T + m2 * 60)); fi
		rm -f "$work/powered-off"
		EMULATOR_VM_CONF=/nonexistent NOW=$n UPTIME_SECONDS=$((n - boot)) STATE_DIR="$work/state" KEEP_MTIME=$T \
			KEEPALIVE_FILE="$work/state/keepalive-abs" PS_CMD="cat $work/ps.txt" MTIME_CMD="$work/mtime" \
			POWEROFF_CMD="$work/poweroff" IDLE_MINUTES=60 MAX_RUNTIME_HOURS=10 \
			"$here/idle-shutdown.sh" > "$work/out" 2>&1
		if [ -e "$work/powered-off" ]; then off[k]=yes; else off[k]=no; fi
	done
}

expect_seq() { # expect_seq <description> <wanted at the first run> <wanted at the second>
	if [ "${off[1]}" = "$2" ] && [ "${off[2]}" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1 (first: ${off[1]} wanted $2, second: ${off[2]} wanted $3)"; echo "     $(cat "$work/out")"; fail=1; fi
}

seq_run keepalive 56 61
expect_seq "a keepalive stops the instance an hour after it was touched, not two (checks at 56 and 61 minutes)" no yes

seq_run keepalive 40 59
expect_seq "a keepalive touched 59 minutes ago does not stop it yet" no no

seq_run pts 56 61
expect_seq "a terminal stops the instance an hour after its last use, not two (checks at 56 and 61 minutes)" no yes

seq_run pts 40 59
expect_seq "a terminal last used 59 minutes ago does not stop it yet" no no

seq_run none 10 61
expect_seq "with no use at all it stops an hour after boot, however the checks fall" no yes

# An active session resets the idle clock.
run 7200 120 "$quiet
sshd: ubuntu@notty"
recorded=$(cat "$work/state/last-active")
if [ "$recorded" = "$NOW" ]; then echo "ok   activity resets the idle clock"; else echo "FAIL activity did not reset the idle clock ($recorded)"; fail=1; fi

exit "$fail"
