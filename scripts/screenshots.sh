#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Make the F-Droid listing screenshots, into
# android/fastlane/metadata/android/en-US/images/phoneScreenshots/.
#
#   scripts/screenshots.sh
#
# android/app/src/androidTest/.../ScreenshotsTest.kt renders each screen from sample data
# and draws it to a PNG from inside the test, so the pictures do not depend on the
# emulator's display: no sleeping screen, no keyguard, no status bar, and nothing is typed
# or tapped. This script starts an emulator, installs the debug app and its test APK, runs
# that test with "am instrument", and pulls the files.
#
# Needs: ANDROID_HOME with emulator, platform-tools and a system image; a JDK; Go and
# gomobile on PATH (the app links a gomobile .aar); a few GB of disk (the emulator refuses
# to start without). Run it after a UI change, and look at what comes out: screenshots
# catch things tests do not, such as clipped text and stale wording.
set -euo pipefail

: "${ANDROID_HOME:?set ANDROID_HOME}"
ADB="$ANDROID_HOME/platform-tools/adb"
AVD="${AVD:-shots}"
APPID=com.github.muelli.synctang.debug
OUT="android/fastlane/metadata/android/en-US/images/phoneScreenshots"
SYSTEM_IMAGE="${SYSTEM_IMAGE:-system-images;android-34;google_apis;x86_64}"

# Every adb command below, installs and all, goes to this emulator and to nothing else.
# Without a serial adb uses the one device that is attached, and when an emulator failed
# to start that was a real phone: it got the debug app, demo mode and a picture of its
# home screen.
EMU_PORT="${EMU_PORT:-5584}"
export ANDROID_SERIAL="emulator-$EMU_PORT"

# Resolved once rather than globbed at each call site: the cmdline-tools directory carries
# a version in its name, so the path is not fixed.
AVDMANAGER=$(find "$ANDROID_HOME/cmdline-tools" -name avdmanager -type f | head -1)
[ -n "$AVDMANAGER" ] || { echo "avdmanager not found under $ANDROID_HOME/cmdline-tools" >&2; exit 1; }

if ! "$AVDMANAGER" list avd | grep -q "Name: $AVD\$"; then
	echo "creating AVD $AVD"
	echo no | "$AVDMANAGER" create avd -n "$AVD" -k "$SYSTEM_IMAGE" -d pixel_6 --force
fi

# Built before the emulator starts, so the build and the emulator do not compete for CPUs.
( cd android && ./gradlew :app:assembleDebug :app:assembleDebugAndroidTest \
	-Psynctang.goMobileTarget=android/amd64 )
APK=android/app/build/outputs/apk/debug/app-debug.apk
TEST_APK=android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
[ -f "$APK" ] && [ -f "$TEST_APK" ] || { echo "the APKs were not built: $APK $TEST_APK" >&2; exit 1; }

# Where the emulator draws. With -no-window its display pipeline can be unusable on a small
# machine (PixelCopy fails, screenshots are black), so by default it gets a window on a
# virtual X display, Xvfb; EMU_XVFB=0 goes back to -no-window.
WINDOW_ARGS=(-no-window)
XVFB_PID=""
if [ "${EMU_XVFB:-1}" = 1 ] && command -v Xvfb > /dev/null; then
	Xvfb :77 -screen 0 1280x1024x24 > /dev/null 2>&1 &
	XVFB_PID=$!
	export DISPLAY=:77
	WINDOW_ARGS=()
	sleep 2
fi
"$ANDROID_HOME/emulator/emulator" -avd "$AVD" -port "$EMU_PORT" "${WINDOW_ARGS[@]}" -gpu "${EMU_GPU:-swiftshader_indirect}" \
	-no-snapshot -no-audio -no-boot-anim -memory 3072 -cores 2 &
EMULATOR_PID=$!
trap 'kill "$EMULATOR_PID" 2>/dev/null || true; [ -z "$XVFB_PID" ] || kill "$XVFB_PID" 2>/dev/null || true' EXIT

echo "waiting for boot"
BOOTED=""
for _ in $(seq 1 60); do
	kill -0 "$EMULATOR_PID" 2>/dev/null || { echo "the emulator exited; see its output above" >&2; exit 1; }
	if [ "$("$ADB" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ]; then
		BOOTED=1
		break
	fi
	sleep 10
done
[ -n "$BOOTED" ] || { echo "the emulator did not finish booting in ten minutes" >&2; exit 1; }

# The emulator's package verifier waits for a Play Protect answer that does not come on a
# fresh, offline image, and the install fails with "Integrity verification timed out".
# This emulator only, by the serial pinned above.
"$ADB" shell settings put global verifier_verify_adb_installs 0
"$ADB" shell settings put global package_verifier_enable 0

"$ADB" install -r "$APK"
"$ADB" install -r "$TEST_APK"

# Not "gradle connectedAndroidTest": that uninstalls the app afterwards, and the pictures
# live in the app's own directory.
#
# sys.boot_completed comes a little before the system is calm, and a test started at once can
# fail with "failed to attach"; so a short wait, and a few tries.
sleep 30
PASSED=""
for attempt in 1 2 3; do
	"$ADB" shell am instrument -w -e class com.github.muelli.synctang.ScreenshotsTest \
		"$APPID.test/androidx.test.runner.AndroidJUnitRunner" | tee /tmp/screenshots-instrument.log
	if grep -q '^OK ' /tmp/screenshots-instrument.log; then
		PASSED=1
		break
	fi
	echo "attempt $attempt did not pass; trying again" >&2
	sleep 20
done
[ -n "$PASSED" ] || { echo "the screenshot test did not pass" >&2; exit 1; }

mkdir -p "$OUT"
for n in 1 2 3 4 5 6; do
	"$ADB" pull "/sdcard/Android/data/$APPID/files/screenshots/$n.png" "$OUT/$n.png"
done

echo
echo "Review these before committing them. Screenshots are the part of the"
echo "listing people look at, and they are also where UI regressions show up"
echo "first: clipped strings, stale wording, a screen that no longer exists."
