#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Boot the disposable VM built by make-test-vm.sh, with its serial console on a
# local TCP socket so a test harness can both watch the boot and type at the
# LUKS prompt.
#
# The console is the point. Every boot-time acceptance item this VM exists for
# (A3, A5, A7, A8) is about what happens before the root filesystem is mounted,
# which is before sshd exists, so the serial console is the only channel that
# can observe it.

set -euo pipefail

OUTDIR="${OUTDIR:-/var/tmp/synctang-vm}"
DISK="$OUTDIR/disk.img"
CONSOLE_PORT="${CONSOLE_PORT:-7100}"
# Whether QEMU waits for a console client before starting the guest. Off by
# default (a VM you just want running should not block on nobody watching), but
# essential for a test harness: everything interesting in these tests happens in
# the first few seconds, and a console attached afterwards has already missed it
# with no way to ask for a replay.
CONSOLE_WAIT="${CONSOLE_WAIT:-0}"
SSH_PORT="${SSH_PORT:-7122}"
MEM="${MEM:-2048}"
NET="${NET:-user}"
# A monitor socket, so the VM can be shut down cleanly with "system_powerdown"
# (ACPI, the virtual power button) instead of being killed. Logging in over the
# serial console to type poweroff works, but it is fiddly to drive from a script
# and pointless when the hypervisor can just press the button.
MONITOR_SOCK="${MONITOR_SOCK:-$OUTDIR/monitor.sock}"
# A second serial port, carrying nothing but the journal.
#
# The console is where a human watches a boot, and it stays readable
# only if it is not also carrying every debug record systemd emits. But
# the journal is exactly what is wanted when something goes wrong
# before the disk is unlocked, and at that point it cannot be read the
# usual way: journalctl needs the root filesystem that is not mounting.
# journald forwards to /dev/ttyS1 (see make-test-vm.sh's kernel command
# line) and this puts ttyS1 on its own socket, so the two streams can
# be captured separately.
JOURNAL_PORT="${JOURNAL_PORT:-7101}"
TAP="${TAP:-sytap0}"
SMP="${SMP:-2}"

if [ ! -f "$DISK" ]; then
	echo "no image at $DISK; run make-test-vm.sh first" >&2
	exit 1
fi

ACCEL="tcg"
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
	ACCEL="kvm"
fi
CONSOLE_WAIT_OPT=",nowait"
if [ "$CONSOLE_WAIT" = "1" ]; then
	CONSOLE_WAIT_OPT=""
	echo "waiting for a console client on 127.0.0.1:$CONSOLE_PORT before starting the guest" >&2
fi

echo "booting with accel=$ACCEL, console on 127.0.0.1:$CONSOLE_PORT, journal on 127.0.0.1:$JOURNAL_PORT, ssh on 127.0.0.1:$SSH_PORT" >&2

# NET=user is QEMU's user-mode networking: enough for the machine to reach the
# Internet (and so the relay pool), and it forwards a port for ssh once the VM
# is booted, but it cannot carry multicast, so local discovery cannot work under
# it at all.
#
# NET=tap puts the VM on the private bridge that test-vm-net.sh creates, sharing
# a real layer 2 segment with this host. That is what local discovery needs, and
# that bridge has no route off it, so it also reproduces A11's condition (a LAN
# and no Internet) without having to break anything.
case "$NET" in
	tap)
		NETDEV=(-netdev "tap,id=net0,ifname=$TAP,script=no,downscript=no")
		;;
	user)
		NETDEV=(-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22")
		;;
	*)
		echo "NET must be tap or user, got '$NET'" >&2
		exit 1
		;;
esac

exec qemu-system-x86_64 \
	-accel "$ACCEL" \
	-m "$MEM" -smp "$SMP" \
	-drive "file=$DISK,format=raw,if=virtio" \
	"${NETDEV[@]}" \
	-device virtio-net-pci,netdev=net0 \
	-serial "telnet:127.0.0.1:$CONSOLE_PORT,server$CONSOLE_WAIT_OPT" \
	-serial "telnet:127.0.0.1:$JOURNAL_PORT,server,nowait" \
	-monitor "unix:$MONITOR_SOCK,server,nowait" \
	-display none \
	"$@"
