#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Create an AVD, boot it headless, and wait until Android says it has
# finished booting. Every wait is bounded and every failure prints the
# emulator's own log, because the failure this exists for was an emulator
# that died at once while `adb wait-for-device` waited for it for an hour.
#
#   scripts/ci-boot-emulator.sh [avd-name]
#
# Environment (all optional except ANDROID_HOME):
#   BOOT_TIMEOUT   seconds to wait for boot to finish (default 600)
#   POLL           seconds between checks (default 5)
#   EMULATOR_LOG   where the emulator's output goes (default /tmp/emulator.log)
#   EMULATOR, ADB, AVDMANAGER   override the tools, for tests
set -euo pipefail

: "${ANDROID_HOME:?set ANDROID_HOME}"
avd=${1:-ci}
boot_timeout=${BOOT_TIMEOUT:-600}
poll=${POLL:-5}
log=${EMULATOR_LOG:-/tmp/emulator.log}
emulator=${EMULATOR:-$ANDROID_HOME/emulator/emulator}
adb=${ADB:-adb}
avdmanager=${AVDMANAGER:-$(find "$ANDROID_HOME/cmdline-tools" -name avdmanager -type f 2>/dev/null | head -1)}

[ -n "$avdmanager" ] || { echo "avdmanager not found under $ANDROID_HOME/cmdline-tools" >&2; exit 1; }

# avdmanager and the emulator each work out where AVDs live from the
# environment, and on a GitHub runner they disagreed: the emulator died with
# "Unknown AVD name [ci]" right after avdmanager had apparently succeeded.
# Name one directory and give it to both.
export ANDROID_AVD_HOME=${ANDROID_AVD_HOME:-$HOME/.android/avd}
mkdir -p "$ANDROID_AVD_HOME"

echo no | "$avdmanager" create avd -n "$avd" -k "system-images;android-34;google_apis;x86_64" -d pixel_6 --force

# avdmanager exits 0 in some situations where it has made nothing; stop here
# with the facts rather than start an emulator that cannot find its AVD.
if [ ! -e "$ANDROID_AVD_HOME/$avd.ini" ]; then
	echo "avdmanager did not create $ANDROID_AVD_HOME/$avd.ini" >&2
	echo "AVD files found anywhere under \$HOME:" >&2
	find "$HOME" -name '*.ini' -path '*avd*' 2>/dev/null | head -20 >&2 || true
	echo "ANDROID_USER_HOME=${ANDROID_USER_HOME:-} ANDROID_SDK_HOME=${ANDROID_SDK_HOME:-} HOME=$HOME" >&2
	exit 1
fi

# Informational: says whether KVM is usable, which is the first thing to
# look at when the emulator will not start.
timeout 30 "$emulator" -accel-check || echo "accel-check reported a problem, see above" >&2

# No window, no audio, no snapshot: a clean boot every time. Software
# rendering is fine with KVM and CPUs that nothing else is using.
nohup "$emulator" -avd "$avd" -no-window -gpu swiftshader_indirect \
	-no-snapshot -no-audio -no-boot-anim -accel on -memory 4096 -cores 3 \
	> "$log" 2>&1 &
pid=$!

dump_log() {
	echo "--- last lines of the emulator log ($log)" >&2
	tail -60 "$log" >&2 || true
}

deadline=$((SECONDS + boot_timeout))
until [ "$(timeout 10 "$adb" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ]; do
	if ! kill -0 "$pid" 2>/dev/null; then
		echo "the emulator process exited before the device finished booting" >&2
		dump_log
		exit 1
	fi
	if [ "$SECONDS" -ge "$deadline" ]; then
		echo "the device did not finish booting within ${boot_timeout}s" >&2
		timeout 10 "$adb" devices >&2 || true
		dump_log
		exit 1
	fi
	sleep "$poll"
done
echo "booted after about $((boot_timeout - (deadline - SECONDS)))s"

# Animations make Compose tests flaky and the system slower; a stalled
# system dialog over the app breaks UI tests.
for k in window_animation_scale transition_animation_scale animator_duration_scale; do
	"$adb" shell settings put global "$k" 0
done
"$adb" shell settings put global hide_error_dialogs 1
