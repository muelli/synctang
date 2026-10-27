#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Make every initrd image that may carry the machine's identity readable by
# root only. The identity (/etc/unlocker/machine.pem) is baked into the initrd
# so the agent can run before the root filesystem is unlocked; anyone who can
# read the image can pose as the machine (docs/threat-model.md).
#
# dracut 110 builds images under umask 077 `[V]` (read in its source), so
# this is a safety net for what it does not cover: an image copied or built
# by something else, or left over from an older setup. It restricts what it
# can and fails loudly where it cannot, because a filesystem that ignores
# chmod (a FAT boot partition mounted without a restrictive umask) would
# otherwise leave the secret readable while everything looked fine.
#
#   BOOT_DIR   where the images are (default /boot)
#   IDENTITY   the machine identity whose presence makes this matter
set -eu

boot=${BOOT_DIR:-/boot}
identity=${IDENTITY:-/etc/unlocker/machine.pem}

# No identity, nothing secret was baked in.
[ -e "$identity" ] || exit 0
[ -d "$boot" ] || exit 0

status=0
for image in "$boot"/initrd.img-* "$boot"/initramfs-*.img; do
	[ -f "$image" ] || continue
	mode=$(stat -c %a "$image")
	case "$mode" in
	600 | 400) continue ;;
	esac
	chmod 0600 "$image" 2>/dev/null || true
	now=$(stat -c %a "$image")
	case "$now" in
	600 | 400)
		echo "synctang: restricted $image to root only (was $mode)"
		;;
	*)
		echo "synctang: WARNING: $image is mode $now and holds this machine's identity, and chmod did not change that." >&2
		echo "synctang: the filesystem ignores permissions; mount it with a restrictive umask (for example umask=0077 in /etc/fstab)" >&2
		status=1
		;;
	esac
done
exit "$status"
