#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Capture the F-Droid listing screenshots from a headless emulator, into
# android/fastlane/metadata/android/en-US/images/phoneScreenshots/.
#
#   scripts/screenshots.sh
#
# Needs: ANDROID_HOME with emulator, platform-tools and a system image;
# a JDK; Go and gomobile on PATH (the app links a gomobile .aar).
#
# Run this after a UI change rather than editing the PNGs by hand, and
# look at what comes out: screenshots catch things tests do not, such as
# clipped text and stale wording.
#
# Known to need a machine that can actually run an emulator. Software
# rendering (swiftshader) on a small cloud container ANRs system_server
# and the launcher continuously, and every capture ends up with an
# "isn't responding" dialog over it. KVM plus a real GPU, or simply more
# cores, is the difference between this working and not.
set -euo pipefail

: "${ANDROID_HOME:?set ANDROID_HOME}"
ADB="$ANDROID_HOME/platform-tools/adb"
AVD="${AVD:-shots}"
APPID=com.github.muelli.synctang
OUT="android/fastlane/metadata/android/en-US/images/phoneScreenshots"
SYSTEM_IMAGE="${SYSTEM_IMAGE:-system-images;android-34;google_apis;x86_64}"

mkdir -p "$OUT"

# Resolved once rather than globbed at each call site: the cmdline-tools
# directory carries a version in its name, so the path is not fixed.
AVDMANAGER=$(find "$ANDROID_HOME/cmdline-tools" -name avdmanager -type f | head -1)
[ -n "$AVDMANAGER" ] || { echo "avdmanager not found under $ANDROID_HOME/cmdline-tools" >&2; exit 1; }

if ! "$AVDMANAGER" list avd | grep -q "Name: $AVD\$"; then
	echo "creating AVD $AVD"
	echo no | "$AVDMANAGER" create avd -n "$AVD" -k "$SYSTEM_IMAGE" -d pixel_6 --force
fi

# -memory/-cores well above the defaults: the stock allocation cannot
# keep up with software rendering and the whole system starts throwing
# "isn't responding" dialogs, which land in the screenshots.
"$ANDROID_HOME/emulator/emulator" -avd "$AVD" -no-window -gpu swiftshader_indirect \
	-no-snapshot -no-audio -no-boot-anim -memory 4096 -cores 4 &
EMULATOR_PID=$!
trap 'kill "$EMULATOR_PID" 2>/dev/null || true' EXIT

echo "waiting for boot"
for _ in $(seq 1 60); do
	[ "$("$ADB" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = "1" ] && break
	sleep 10
done

# The emulator has no biometric enrolled, and the key holder's key is
# gated behind one, so the real build cannot get past its first screen
# here. The debug-only ungated variant renders these screens identically
# (the biometric prompt only appears when answering an unlock), so it is
# what gets photographed; it uses a separate Keystore alias and cannot
# exist in a release build. See TestMode.
( cd android && ./gradlew :app:assembleDebug \
	-Psynctang.goMobileTarget=android/amd64 -Psynctang.noBiometric=true )

"$ADB" install -r -g android/app/build/outputs/apk/debug/app-debug.apk

# A clean status bar, per SystemUI's demo mode: a real clock and a
# half-full battery date the screenshots and distract from the app.
"$ADB" shell settings put global sysui_demo_allowed 1
demo() { "$ADB" shell am broadcast -a com.android.systemui.demo -e command "$@" >/dev/null; }
demo enter
demo clock -e hhmm 0900
demo network -e wifi show -e level 4 -e mobile hide
demo battery -e level 100 -e plugged false
demo notifications -e visible false

shoot() { # shoot <n> <description>
	sleep 4
	"$ADB" exec-out screencap -p > "$OUT/$1.png"
	echo "captured $OUT/$1.png ($2)"
}

# Generating the long-term key takes a while under software rendering,
# and the first screen shows a spinner until it finishes.
"$ADB" shell am force-stop "$APPID"
"$ADB" shell am start -n "$APPID/.MainActivity" >/dev/null
sleep 40
shoot 1 "pairing screen: scan a machine's QR code"

# The manual field takes the same payload the QR code carries. Without a
# psk= it only records which machine to dial, which needs no network and
# no machine, and is enough to reach the unlock screen.
"$ADB" shell input tap 540 1500
"$ADB" shell input text 'synctang://pair?machine=YIEAQU3-4EQIMH3-INVK5OV-QEPJZSW-GPKQF5R-UUGZZR2-KBIPPXD-BLGZ2AG%sname=study%sworkstation'
"$ADB" shell input keyevent KEYCODE_ENTER
shoot 2 "unlock screen: paired, waiting for the machine"

echo
echo "Review these before committing them. Screenshots are the part of the"
echo "listing people look at, and they are also where UI regressions show up"
echo "first: clipped strings, stale wording, a screen that no longer exists."
