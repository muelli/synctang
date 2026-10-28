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
#
# Like the real tools, the fake avdmanager writes the AVD under
# $ANDROID_AVD_HOME when that is set and under $ANDROID_USER_HOME/avd
# otherwise (the CI runner's environment can differ from a developer's), and
# the fake emulator looks only under $ANDROID_AVD_HOME or $HOME/.android/avd.
# FAKE_AVDMANAGER=silent makes avdmanager exit 0 without creating anything.
cat > "$work/avdmanager" <<'SH'
#!/bin/bash
cat > /dev/null
[ "${FAKE_AVDMANAGER:-}" != silent ] || exit 0
while [ $# -gt 0 ]; do [ "$1" != -n ] || name=$2; shift; done
dir=${ANDROID_AVD_HOME:-${ANDROID_USER_HOME:-$HOME/.android}/avd}
mkdir -p "$dir"
echo "avd.ini.encoding=UTF-8" > "$dir/$name.ini"
exit 0
SH
cat > "$work/emulator" <<'SH'
#!/bin/bash
avd_dir=${ANDROID_AVD_HOME:-$HOME/.android/avd}
case "$1" in
-accel-check) echo "accel: fake"; exit 0 ;;
-list-avds) for f in "$avd_dir"/*.ini; do [ -e "$f" ] && basename "$f" .ini; done; exit 0 ;;
-avd) [ -e "$avd_dir/$2.ini" ] || { echo "ERROR | Unknown AVD name [$2], use -list-avds to see valid list." >&2; exit 1; } ;;
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
	rm -rf "$work/checks" "$work/adb-calls" "$work/emulator.log" "${work:?}/home" "${work:?}/elsewhere"
	mkdir -p "$work/home"
	set +e
	env -u ANDROID_AVD_HOME HOME="$work/home" ${FAKE_USER_HOME:+ANDROID_USER_HOME="$FAKE_USER_HOME"} \
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

# The failure seen on a GitHub runner: avdmanager created the AVD somewhere
# the emulator did not look ("Unknown AVD name [ci]"). Both must be pointed at
# one directory, whatever the environment says.
FAKE_USER_HOME="$work/elsewhere" run boots 30
check "an AVD is found even when ANDROID_USER_HOME points elsewhere" succeeded

# And if avdmanager says nothing and creates nothing, the script must stop
# there and say so, not start an emulator that cannot find its AVD.
FAKE_AVDMANAGER=silent run boots 30
check "an avdmanager that creates nothing fails" failed
check "an avdmanager that creates nothing is reported" grep -q 'did not create' "$work/out"
check "an avdmanager that creates nothing never starts the emulator" test ! -e "$work/emulator.log"

exit "$fail"
