#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Enrol a virtual fingerprint on an emulator, through the Settings screens, for a probe of what a key that needs a
# fingerprint can do. Needs a userdebug image (adb root), the PIN 1234 already set, and the emulator's own
# fingerprint service. Every wait is bounded; what it sees on the way is printed.
#
#   scripts/ci-enroll-fingerprint.sh
set -uo pipefail

adb=${ADB:-adb}
pin=${PIN:-1234}
work=$(mktemp -d)
quiet=0
trap 'rm -rf "$work"' EXIT

# The screen on and staying so: a lock screen in the way is what stopped an earlier run.
$adb shell svc power stayon true
$adb shell settings put system screen_off_timeout 1800000
$adb shell input keyevent KEYCODE_WAKEUP
$adb shell wm dismiss-keyguard
sleep 1
$adb shell am start -a android.settings.BIOMETRIC_ENROLL > /dev/null

for i in $(seq 1 80); do
	$adb shell uiautomator dump /sdcard/ui.xml > /dev/null 2>&1
	$adb pull /sdcard/ui.xml "$work/ui.xml" > /dev/null 2>&1 || { sleep 2; continue; }
	# What to press on this screen: the PIN field gets the PIN, a button with one of the usual words is tapped.
	action=$(python3 -I - "$work/ui.xml" <<'PY'
import re, sys
import xml.etree.ElementTree as ET
tree = ET.parse(sys.argv[1])
nodes = []
for n in tree.iter('node'):
    text = (n.get('text') or n.get('content-desc') or '').strip()
    m = re.match(r'\[(\d+),(\d+)\]\[(\d+),(\d+)\]', n.get('bounds') or '')
    if not m:
        continue
    x1, y1, x2, y2 = map(int, m.groups())
    nodes.append((text, n.get('clickable') == 'true', n.get('class') or '', (x1 + x2) // 2, (y1 + y2) // 2, n.get('resource-id') or ''))
alltext = ' | '.join(t for t, *_ in nodes if t)
# The lock screen ("Enter PIN" over a keypad) and Settings' own check ("Confirm your PIN") both want the PIN typed.
if re.search(r'(?i)\b(enter|confirm|re-?enter)\b.{0,20}\b(pin|pattern|password)\b|\byour (pin|pattern|password)\b', alltext):
    print('pin')
    sys.exit()
words = ['I agree', 'Agree', 'Yes, I’m in', "Yes, I'm in", 'Next', 'More', 'Start', 'Continue', 'Done', 'Skip', 'OK', 'Got it', 'Not now']
for w in words:
    for text, clickable, cls, x, y, rid in nodes:
        if text.lower() == w.lower() and (clickable or cls.endswith('Button')):
            print(f'tap {x} {y} {w}')
            sys.exit()
print('touch ' + alltext[:150])
PY
)
	echo "step $i: ${action:0:140}"
	case "$action" in
	pin) $adb shell input text "$pin"; $adb shell input keyevent KEYCODE_ENTER ;;
	tap*) read -r _ x y _ <<< "$action"; $adb shell input tap "$x" "$y" ;;
	esac
	# A touch of the virtual sensor each step: harmless where nothing waits for one, and what enrolment needs.
	$adb emu finger touch 1 > /dev/null 2>&1
	# Done when the screens are no longer about a fingerprint: enrolment ended and Settings is back to what it was.
	# Whether it worked is for the test that needs it to say (BiometricManager.canAuthenticate).
	if [ "$i" -ge 8 ]; then
		case "$action" in
		touch*[Ff]inger* | touch*[Ss]ensor* | tap*) quiet=0 ;;
		*) quiet=$((quiet + 1)) ;;
		esac
		if [ "$quiet" -ge 3 ]; then echo "the enrolment screens are gone after step $i"; exit 0; fi
	fi
	sleep 1
done
echo "the enrolment screens were still there after 80 steps" >&2
exit 1
