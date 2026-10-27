#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests dracut/90unlocker/secure-initrd.sh against a temporary /boot.
#
# The helpers below are called through check, which shellcheck cannot see.
# shellcheck disable=SC2329
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../dracut/90unlocker/secure-initrd.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

# run <identity present: yes|no> [extra PATH dir]; output in $work/out, status in $rc.
run() {
	local identity="$work/machine.pem"
	if [ "$1" = yes ]; then : > "$identity"; else rm -f "$identity"; fi
	set +e
	PATH="${2:+$2:}$PATH" BOOT_DIR="$work/boot" IDENTITY="$identity" "$script" > "$work/out" 2>&1
	rc=$?
	set -e
}

mode() { stat -c %a "$work/boot/$1"; }

check() { # check <description> <command...>: passes when the command succeeds
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

mode_is() { [ "$(mode "$1")" = "$2" ]; }
said() { grep -q -- "$1" "$work/out"; }
succeeded() { [ "$rc" -eq 0 ]; }
failed() { [ "$rc" -ne 0 ]; }
fresh_boot() {
	rm -rf "${work:?}/boot"
	mkdir -p "$work/boot"
	: > "$work/boot/initrd.img-7.0.0-1"
	: > "$work/boot/initramfs-7.0.0-2.img"
	: > "$work/boot/vmlinuz-7.0.0-1"
	chmod 644 "$work/boot/initrd.img-7.0.0-1" "$work/boot/initramfs-7.0.0-2.img" "$work/boot/vmlinuz-7.0.0-1"
}

echo "== the identity is in the initrd"
fresh_boot
run yes
check "succeeds" succeeded
check "makes a world-readable Debian-style initrd owner-only" mode_is initrd.img-7.0.0-1 600
check "makes a world-readable Fedora-style initramfs owner-only" mode_is initramfs-7.0.0-2.img 600
check "leaves the kernel image alone" mode_is vmlinuz-7.0.0-1 644
check "says what it changed" said "restricted"

echo "== already owner-only"
fresh_boot
chmod 400 "$work/boot/initrd.img-7.0.0-1"
chmod 600 "$work/boot/initramfs-7.0.0-2.img"
run yes
check "succeeds" succeeded
check "does not loosen a stricter mode" mode_is initrd.img-7.0.0-1 400
check "says nothing when there is nothing to change" bash -c "! grep -q restricted '$work/out'"

echo "== no identity was ever baked in"
fresh_boot
run no
check "succeeds" succeeded
check "touches nothing" mode_is initrd.img-7.0.0-1 644

echo "== a filesystem that ignores chmod (a FAT boot partition)"
fresh_boot
mkdir -p "$work/shim"
printf '#!/bin/sh\nexit 0\n' > "$work/shim/chmod"
chmod +x "$work/shim/chmod"
run yes "$work/shim"
check "fails" failed
check "names the file it could not protect" said "initrd.img-7.0.0-1"
check "explains what to do" said "umask"

echo "== no boot directory"
rm -rf "${work:?}/boot"
run yes
check "succeeds" succeeded

exit "$fail"
