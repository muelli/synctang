#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build a small disposable Ubuntu 26.04 VM to act as a key holder, the
# counterpart of the machine that make-test-vm.sh builds. Its root is plain
# (no LUKS): it is the one that stays up and answers, not the one that has to
# be unlocked. No synctang software is installed: the test installs the
# synctang-keyholder package inside the booted VM and initialises the key
# there, so the package is what is being proved and the key and transport
# identity are created where they will be used.
#
# Needs root, because it loop-mounts and chroots. Everything it touches lives
# under $OUTDIR. It reuses the root filesystem tarball make-test-vm.sh
# downloaded when ROOTFS_TAR points at it.
#
#   sudo OUTDIR=/var/tmp/synctang-kh \
#        ROOTFS_TAR=/var/tmp/synctang-vm/rootfs.tar.xz scripts/make-keyholder-vm.sh

set -euo pipefail

OUTDIR="${OUTDIR:-/var/tmp/synctang-kh}"
DISK="$OUTDIR/disk.img"
DISK_SIZE="${DISK_SIZE:-6G}"
ROOTFS_TAR="${ROOTFS_TAR:-$OUTDIR/rootfs.tar.xz}"
ROOTFS_URL="${ROOTFS_URL:-https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64-root.tar.xz}"
MNT="$OUTDIR/mnt"
# A login for looking around from the serial console; ssh uses the key in
# $OUTDIR/authorized_keys. A fixture, not a secret: this VM holds nothing.
VM_PASSWORD="${VM_PASSWORD:-synctang-test-passphrase}"

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
	for d in dev/pts dev proc sys run boot; do
		mountpoint -q "$MNT/$d" && umount -l "$MNT/$d"
	done
	mountpoint -q "$MNT" && umount -l "$MNT"
	[ -n "$LOOP" ] && losetup -d "$LOOP"
	return 0
}
trap cleanup EXIT

log "creating a $DISK_SIZE disk"
rm -f "$DISK"
truncate -s "$DISK_SIZE" "$DISK"
# BIOS boot, as in make-test-vm.sh: p1 is GRUB's BIOS boot partition, p2 the root.
sgdisk --clear \
	--new=1:0:+2M --typecode=1:ef02 --change-name=1:biosboot \
	--new=2:0:0 --typecode=2:8300 --change-name=2:root \
	"$DISK" > /dev/null
LOOP=$(losetup --find --show --partscan "$DISK")
mkfs.ext4 -q -L root "${LOOP}p2"
mount "${LOOP}p2" "$MNT"

log "unpacking the root filesystem"
tar -xpf "$ROOTFS_TAR" -C "$MNT" --numeric-owner --xattrs-include='*'

ROOT_UUID=$(blkid -s UUID -o value "${LOOP}p2")
echo "UUID=$ROOT_UUID / ext4 defaults 0 1" > "$MNT/etc/fstab"
touch "$MNT/etc/cloud/cloud-init.disabled"
echo "synctang-keyholder" > "$MNT/etc/hostname"

# Plain DHCP on whatever interface exists. The test bridge has a dnsmasq on it
# and no route off it, so this VM, like the machine, has a LAN and no Internet.
mkdir -p "$MNT/etc/netplan"
rm -f "$MNT"/etc/netplan/*.yaml
cat > "$MNT/etc/netplan/50-synctang.yaml" <<'NETPLAN'
network:
  version: 2
  ethernets:
    all:
      match:
        name: "en*"
      dhcp4: true
      dhcp6: false
NETPLAN
chmod 600 "$MNT/etc/netplan/50-synctang.yaml"

mount --bind /dev "$MNT/dev"
mount --bind /dev/pts "$MNT/dev/pts"
mount -t proc proc "$MNT/proc"
mount -t sysfs sys "$MNT/sys"
mount -t tmpfs tmpfs "$MNT/run"
rm -f "$MNT/etc/resolv.conf"
cp /etc/resolv.conf "$MNT/etc/resolv.conf"

log "installing a kernel, GRUB and ssh inside the image"
cat > "$MNT/tmp/setup.sh" <<CHROOT
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq grub-pc openssh-server systemd-resolved ca-certificates
apt-get install -y -qq linux-image-generic
cat > /etc/default/grub <<'GRUB'
GRUB_DEFAULT=0
GRUB_TIMEOUT=1
GRUB_DISTRIBUTOR="synctang-keyholder"
GRUB_CMDLINE_LINUX="console=tty1 console=ttyS0,115200"
GRUB_TERMINAL="console serial"
GRUB_SERIAL_COMMAND="serial --speed=115200"
GRUB
grub-install --target=i386-pc "$LOOP"
update-grub
# The cloud image ships no host keys (cloud-init normally makes them, and it
# is disabled here), and sshd refuses to start without any.
ssh-keygen -A
systemctl enable ssh
systemctl enable serial-getty@ttyS0.service
echo "root:$VM_PASSWORD" | chpasswd
useradd -m -s /bin/bash -G sudo ubuntu || true
echo "ubuntu:$VM_PASSWORD" | chpasswd
echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/90-test
mkdir -p /home/ubuntu/.ssh
CHROOT
chroot "$MNT" bash /tmp/setup.sh
rm -f "$MNT/tmp/setup.sh"

if [ -f "$OUTDIR/authorized_keys" ]; then
	install -o 1000 -g 1000 -m 600 "$OUTDIR/authorized_keys" "$MNT/home/ubuntu/.ssh/authorized_keys"
fi
chown 1000:1000 "$MNT/home/ubuntu/.ssh"

log "built $DISK"
