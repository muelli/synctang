#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Install this project into the disposable VM built by make-test-vm.sh: the
# unlocker binary, the dracut module, a machine transport identity, and one or
# more enrolled key holders. Then rebuild the initrd so the agent is actually in
# it.
#
# Enrolment happens from the host, against the image's LUKS2 partition over a
# loop device, rather than inside the VM. It is the same cryptsetup operating on
# the same header either way, and doing it here means the VM never has to be
# booted and logged into just to be set up.
#
# Recipients are passed as repeated --recipient PUBKEY:TRANSPORT_ID arguments.

set -euo pipefail

OUTDIR="${OUTDIR:-/var/tmp/synctang-vm}"
DISK="$OUTDIR/disk.img"
MNT="$OUTDIR/mnt"
MACHINE_NAME="${MACHINE_NAME:-synctang-local}"
VM_PASSPHRASE="${VM_PASSPHRASE:-synctang-test-passphrase}"
UNLOCKER="${UNLOCKER:-}"

RECIPIENTS=()
while [ $# -gt 0 ]; do
	case "$1" in
		--recipient) RECIPIENTS+=("$2"); shift 2 ;;
		*) echo "unknown argument: $1" >&2; exit 1 ;;
	esac
done

log() { printf '\n=== %s\n' "$*" >&2; }

if [ "$(id -u)" -ne 0 ]; then
	echo "$0 must run as root (it loop-mounts and chroots)" >&2
	exit 1
fi
if [ -z "$UNLOCKER" ] || [ ! -x "$UNLOCKER" ]; then
	echo "set UNLOCKER to the unlocker binary to install" >&2
	exit 1
fi

LOOP=""
cleanup() {
	set +e
	for d in dev/pts dev proc sys run boot; do
		mountpoint -q "$MNT/$d" && umount -l "$MNT/$d"
	done
	mountpoint -q "$MNT" && umount -l "$MNT"
	[ -e /dev/mapper/synctang_test_crypt ] && cryptsetup close synctang_test_crypt
	[ -n "$LOOP" ] && losetup -d "$LOOP"
	return 0
}
trap cleanup EXIT

LOOP=$(losetup --find --show --partscan "$DISK")
printf '%s' "$VM_PASSPHRASE" | cryptsetup open "${LOOP}p3" synctang_test_crypt -
mount /dev/mapper/synctang_test_crypt "$MNT"
mount "${LOOP}p2" "$MNT/boot"

log "installing the unlocker and the dracut module"
# /usr/sbin, not /usr/local/bin: that is the PATH dpkg maintainer scripts run
# with, which is what a kernel upgrade's initrd rebuild needs in order to find
# the binary at all. The Debian packaging installs it here for the same reason.
install -m 755 "$UNLOCKER" "$MNT/usr/sbin/unlocker"
mkdir -p "$MNT/usr/lib/dracut/modules.d/90unlocker"
install -m 755 "$(dirname "$0")/../dracut/90unlocker"/*.sh \
	"$MNT/usr/lib/dracut/modules.d/90unlocker/"

log "enrolling ${#RECIPIENTS[@]} recipient(s)"
PASSFILE=$(mktemp)
chmod 600 "$PASSFILE"
printf '%s' "$VM_PASSPHRASE" > "$PASSFILE"
trap 'rm -f "$PASSFILE"; cleanup' EXIT

mkdir -p "$MNT/etc/unlocker"
for r in "${RECIPIENTS[@]}"; do
	pubkey="${r%%:*}"
	transport="${r#*:}"
	"$UNLOCKER" enrol \
		--device "${LOOP}p3" \
		--pubkey "$pubkey" \
		--recipient-transport-id "$transport" \
		--existing-passphrase-file "$PASSFILE" \
		--name "$MACHINE_NAME" \
		--machine-key-file "$MNT/etc/unlocker/machine.pem"
done
rm -f "$PASSFILE"

KVER=$(basename "$(find "$MNT/lib/modules" -mindepth 1 -maxdepth 1 -type d | sort | head -1)")
log "rebuilding the initrd for $KVER"
mount --bind /dev "$MNT/dev"
mount --bind /dev/pts "$MNT/dev/pts"
mount -t proc proc "$MNT/proc"
mount -t sysfs sys "$MNT/sys"
mount -t tmpfs tmpfs "$MNT/run"
# A deliberately minimal PATH, matching what dpkg gives a maintainer script.
# Rebuilding under it is what proves the module is findable in the one situation
# that silently broke before: an unattended kernel upgrade.
chroot "$MNT" env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin \
	dracut --force --quiet "/boot/initrd.img-$KVER" "$KVER"

log "checking the initrd actually contains the agent"
chroot "$MNT" lsinitrd "/boot/initrd.img-$KVER" | grep -E 'unlocker' || {
	echo "the rebuilt initrd contains no unlocker files" >&2
	exit 1
}

log "provisioned"
