#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests scripts/ci-boot-emulator.sh with stand-ins for the emulator, adb and
# avdmanager, so its failure paths run in seconds without Android installed.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

# Stand-ins. FAKE_MODE chooses how the "emulator" behaves:
#   dies    exits at once with an error, like the real failure
#   hangs   stays alive but the device never reports boot completed
#   boots   reports boot completed on the third check
cat > "$work/avdmanager" <<'SH'
#!/bin/bash
cat > /dev/null
exit 0
SH
cat > "$work/emulator" <<'SH'
#!/bin/bash
case "$1" in
-accel-check) echo "accel: fake"; exit 0 ;;
esac
case "$FAKE_MODE" in
dies) echo "emulator: ERROR: this fake emulator cannot start" >&2; exit 1 ;;
hangs | boots) echo "emulator: running"; exec sleep 30 ;;
esac
SH
cat > "$work/adb" <<'SH'
#!/bin/bash
counter="$FAKE_DIR/checks"
case "$*" in
"shell getprop sys.boot_completed")
	n=$(cat "$counter" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$counter"
	if [ "$FAKE_MODE" = boots ] && [ "$n" -ge 3 ]; then echo 1; else echo; fi ;;
"devices") echo "List of devices attached" ;;
*) echo "$*" >> "$FAKE_DIR/adb-calls" ;;
esac
exit 0
SH
chmod +x "$work/avdmanager" "$work/emulator" "$work/adb"

run() { # run <mode> <boot timeout>; output goes to $work/out, status to $rc
	rm -f "$work/checks" "$work/adb-calls"
	set +e
	ANDROID_HOME="$work" AVDMANAGER="$work/avdmanager" EMULATOR="$work/emulator" ADB="$work/adb" \
		FAKE_MODE="$1" FAKE_DIR="$work" EMULATOR_LOG="$work/emulator.log" BOOT_TIMEOUT="$2" POLL=1 \
		"$here/ci-boot-emulator.sh" > "$work/out" 2>&1
	rc=$?
	set -e
}

check() { # check <description> <command...>: passes when the command succeeds
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

# Called through check, which shellcheck cannot see.
# shellcheck disable=SC2329
failed() { [ "$rc" -ne 0 ]; }
# shellcheck disable=SC2329
succeeded() { [ "$rc" -eq 0 ]; }

run dies 30
check "an emulator that dies fails" failed
check "an emulator that dies is reported as having exited" grep -q 'exited before' "$work/out"
check "an emulator that dies has its own log printed" grep -q 'cannot start' "$work/out"

run hangs 3
check "an emulator that never boots fails" failed
check "a boot timeout says so" grep -q 'did not finish booting' "$work/out"

run boots 30
check "an emulator that boots succeeds" succeeded
check "booting turns animations off" grep -q 'window_animation_scale 0' "$work/adb-calls"
check "booting hides error dialogs" grep -q 'hide_error_dialogs 1' "$work/adb-calls"

exit "$fail"
