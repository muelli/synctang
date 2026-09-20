#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build a disposable VM with a real LUKS2 root, for boot-time acceptance tests
# (A3, A5, A7, A8) that cannot be done on a loopback image because they are
# about what happens in the initrd.
#
# Why this exists: the project's other test machine is a shared VM on someone
# else's hypervisor, whose passphrase this side does not hold, so any test that
# might leave it unbootable (A8 destroys a keyslot) cannot be run there. This
# one is built from scratch, throwaway, and its passphrase is whatever the
# caller passes in.
#
# Needs root, because it loop-mounts and chroots. Everything it touches lives
# under $OUTDIR; it creates no system state outside that except the loop device
# it detaches on exit.

set -euo pipefail

OUTDIR="${OUTDIR:-/var/tmp/synctang-vm}"
DISK="$OUTDIR/disk.img"
DISK_SIZE="${DISK_SIZE:-6G}"
ROOTFS_TAR="$OUTDIR/rootfs.tar.xz"
ROOTFS_URL="${ROOTFS_URL:-https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64-root.tar.xz}"
MNT="$OUTDIR/mnt"

# The passphrase is a test fixture, not a secret: this disk holds nothing but a
# stock Ubuntu and is rebuilt from scratch by this script. It is still read from
# the environment rather than written here, so that the same script stays usable
# for a machine where it would matter.
VM_PASSPHRASE="${VM_PASSPHRASE:-synctang-test-passphrase}"

# Deliberately cheap key derivation. A stock LUKS2 header targets about two
# seconds of argon2id per keyslot trial, which on the real test VM turned out to
# be 6.9 seconds across three keyslots and dominated every A10 measurement. That
# cost is real and worth measuring once, but paying it on every iteration of a
# boot test measures nothing except argon2. pbkdf2 with a low iteration count
# makes the unlock itself near-instant so that a slow boot means a real bug.
# Never use these parameters for a volume holding anything.
PBKDF_ARGS=(--pbkdf pbkdf2 --pbkdf-force-iterations 1000)

log() { printf '\n=== %s\n' "$*" >&2; }

if [ "$(id -u)" -ne 0 ]; then
	echo "$0 must run as root (it loop-mounts and chroots)" >&2
	exit 1
fi

mkdir -p "$OUTDIR" "$MNT"

if [ ! -f "$ROOTFS_TAR" ]; then
	log "downloading the root filesystem"
	curl -sSL -o "$ROOTFS_TAR" "$ROOTFS_URL"
fi

LOOP=""
cleanup() {
	set +e
	mountpoint -q "$MNT/dev/pts" && umount "$MNT/dev/pts"
	for d in dev proc sys run boot/efi boot; do
		mountpoint -q "$MNT/$d" && umount -l "$MNT/$d"
	done
	mountpoint -q "$MNT" && umount -l "$MNT"
	[ -e /dev/mapper/synctang_test_crypt ] && cryptsetup close synctang_test_crypt
	[ -n "$LOOP" ] && losetup -d "$LOOP"
	return 0
}
trap cleanup EXIT

log "creating a $DISK_SIZE disk"
rm -f "$DISK"
truncate -s "$DISK_SIZE" "$DISK"

# BIOS boot rather than UEFI, so the build needs no OVMF firmware and the VM
# boots on QEMU's built-in SeaBIOS: p1 is GRUB's BIOS boot partition, p2 is an
# unencrypted /boot (GRUB reads the kernel and initrd from here), p3 is the
# LUKS2 container holding everything else.
sgdisk --clear \
	--new=1:0:+2M --typecode=1:ef02 --change-name=1:biosboot \
	--new=2:0:+1G --typecode=2:8300 --change-name=2:boot \
	--new=3:0:0 --typecode=3:8309 --change-name=3:luks \
	"$DISK" > /dev/null

LOOP=$(losetup --find --show --partscan "$DISK")
log "loop device is $LOOP"

log "formatting the LUKS2 container"
printf '%s' "$VM_PASSPHRASE" | cryptsetup luksFormat --type luks2 "${PBKDF_ARGS[@]}" \
	--batch-mode "${LOOP}p3" -
printf '%s' "$VM_PASSPHRASE" | cryptsetup open "${LOOP}p3" synctang_test_crypt -
LUKS_UUID=$(cryptsetup luksUUID "${LOOP}p3")
log "LUKS UUID is $LUKS_UUID"

mkfs.ext4 -q -L boot "${LOOP}p2"
mkfs.ext4 -q -L root /dev/mapper/synctang_test_crypt

mount /dev/mapper/synctang_test_crypt "$MNT"
mkdir -p "$MNT/boot"
mount "${LOOP}p2" "$MNT/boot"

log "unpacking the root filesystem"
tar -xpf "$ROOTFS_TAR" -C "$MNT" --numeric-owner --xattrs-include='*'

BOOT_UUID=$(blkid -s UUID -o value "${LOOP}p2")
ROOT_UUID=$(blkid -s UUID -o value /dev/mapper/synctang_test_crypt)

cat > "$MNT/etc/fstab" <<EOF
UUID=$ROOT_UUID / ext4 defaults 0 1
UUID=$BOOT_UUID /boot ext4 defaults 0 2
EOF

cat > "$MNT/etc/crypttab" <<EOF
synctang_test_crypt UUID=$LUKS_UUID none luks,discard
EOF

# The cloud image ships with cloud-init driving first boot; this VM is
# configured entirely here instead, so it is one less moving part between a
# failed boot and its cause.
touch "$MNT/etc/cloud/cloud-init.disabled"

cat > "$MNT/etc/hostname" <<EOF
synctang-local
EOF

mount --bind /dev "$MNT/dev"
mount --bind /dev/pts "$MNT/dev/pts"
mount -t proc proc "$MNT/proc"
mount -t sysfs sys "$MNT/sys"
mount -t tmpfs tmpfs "$MNT/run"
# The cloud image ships /etc/resolv.conf as a symlink into /run, which is
# dangling in an unbooted image, so copying onto it fails rather than replacing
# it. The chroot needs a working resolver for apt; systemd-resolved restores its
# own symlink on first boot.
rm -f "$MNT/etc/resolv.conf"
cp /etc/resolv.conf "$MNT/etc/resolv.conf"

log "installing a kernel, dracut and GRUB inside the image"
cat > "$MNT/tmp/setup.sh" <<CHROOT
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

# dracut has to be present before the kernel, or the kernel's postinst builds an
# initramfs-tools initrd and this project's dracut module is never consulted.
apt-get update -qq
apt-get install -y -qq dracut dracut-network cryptsetup cryptsetup-initramfs- \
	grub-pc openssh-server systemd-resolved ca-certificates
apt-get install -y -qq linux-image-generic

# Serial console, so the test harness can watch the boot and type at the LUKS
# prompt without any graphics.
cat > /etc/default/grub <<'GRUB'
GRUB_DEFAULT=0
GRUB_TIMEOUT=2
GRUB_DISTRIBUTOR="synctang-local"
GRUB_CMDLINE_LINUX_DEFAULT=""
# forward_to_console makes journald emit every record to a serial port,
# which is the only way to see the journal of a machine that has not
# unlocked its root filesystem yet: journalctl needs the filesystem
# that is not mounting, so an agent can fail every ten seconds for
# hours and say nothing a human can see.
#
# Which port is decided by TTYPath in the journald drop-in below, not
# here: systemd.journald.tty_path is not one of the settings journald
# reads off the kernel command line (only forward_to_* and max_level_*
# are), so passing it there is silently ignored and everything lands on
# the console a human is trying to read.
GRUB_CMDLINE_LINUX="console=tty1 console=ttyS0,115200 rd.luks.uuid=$LUKS_UUID systemd.journald.forward_to_console=1"
GRUB_TERMINAL="console serial"
GRUB_SERIAL_COMMAND="serial --speed=115200"
GRUB
grub-install --target=i386-pc "$LOOP"
update-grub

# The journal goes to the second serial port, so the first stays
# readable for watching a boot. The drop-in has to be pulled into the
# initrd explicitly: dracut installs journald.conf itself but not
# arbitrary drop-ins beside it.
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/99-synctang-test.conf <<'JOURNALD'
[Journal]
ForwardToConsole=yes
TTYPath=/dev/ttyS1
MaxLevelConsole=debug
JOURNALD

mkdir -p /etc/dracut.conf.d
cat > /etc/dracut.conf.d/99-synctang-journal.conf <<'DRACUT'
install_items+=" /etc/systemd/journald.conf.d/99-synctang-test.conf "
DRACUT

systemctl enable ssh
systemctl enable serial-getty@ttyS0.service

# A password login on the serial console, for the console-fallback test (A5)
# and for looking around when a boot goes wrong.
echo "root:$VM_PASSPHRASE" | chpasswd
useradd -m -s /bin/bash -G sudo ubuntu || true
echo "ubuntu:$VM_PASSPHRASE" | chpasswd
mkdir -p /home/ubuntu/.ssh
CHROOT

chroot "$MNT" bash /tmp/setup.sh
rm -f "$MNT/tmp/setup.sh"

if [ -f "$OUTDIR/authorized_keys" ]; then
	install -o 1000 -g 1000 -m 600 "$OUTDIR/authorized_keys" "$MNT/home/ubuntu/.ssh/authorized_keys"
fi

log "built $DISK"
printf 'LUKS UUID: %s\n' "$LUKS_UUID"
