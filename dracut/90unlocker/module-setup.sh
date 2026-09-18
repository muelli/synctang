#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dracut module: fetch a LUKS volume secret during early boot from a human-operated
# key holder (laptop or phone), over the Syncthing relay network. Adapted from
# syncthing-socket's contrib/dracut-luks/90syncthing-socket, which this project's
# transport is built on; see THIRD_PARTY.md.
#
# INSTALLATION: /usr/lib/dracut/modules.d/90unlocker/
#
# Ubuntu 26.04 and later ship dracut rather than initramfs-tools; its initrd has no
# /lib/cryptsetup/askpass and no passfifo to race. It uses systemd-cryptsetup, which
# asks through systemd's password agent protocol, so that is what this answers. The
# design goal: do not replace the prompt, answer alongside it, whoever answers first
# wins. Manual entry, recovery keys and TPM agents all keep working.

# shellcheck disable=SC2154

check() {
	require_binaries unlocker || return 1
	return 0
}

depends() {
	# "crypt" brings the LUKS machinery, "network" the stack needed to reach the
	# relay pool. With systemd present both arrive through dracut-crypt-generator
	# and systemd-cryptsetup rather than a plain cryptsetup keyscript.
	echo "crypt network"
	return 0
}

install() {
	inst_binary unlocker

	# 70crypt installs the cryptsetup binary only on its non-systemd branch: with
	# systemd present it installs dracut-crypt-generator and relies on
	# systemd-cryptsetup instead. The agent reads the LUKS2 header itself (to find
	# its own mr-1 token) and verifies a recovered secret before answering, so
	# without this the binary it needs is simply absent.
	inst_multiple cryptsetup

	# Discovery and the relay pool are reached over HTTPS, so the trust store has
	# to come along.
	if [ -r /etc/ssl/certs/ca-certificates.crt ]; then
		inst_simple /etc/ssl/certs/ca-certificates.crt
	else
		dwarn "unlocker: /etc/ssl/certs/ca-certificates.crt is missing."
		dwarn "unlocker: install ca-certificates, or discovery will fail at boot."
	fi

	# The human who ran "unlocker enrol" already generated this machine's
	# persistent transport identity (transport.LoadOrCreateCert's default
	# path). Carrying the same file into the initrd is what lets the agent
	# present the exact Device ID a key holder's Recipient.Transport was
	# recorded against; without it the agent would generate a fresh identity
	# on first boot and no enrolled key holder would recognise it. Missing
	# entirely just means enrol has not run yet, so there is nothing to copy.
	if [ -r /etc/unlocker/machine.pem ]; then
		inst_simple /etc/unlocker/machine.pem
	fi

	# The initrd's own default network file is already "DHCP=yes" for every
	# non-loopback interface, but networkd is only waited on when rd.neednet is
	# set. Without this the agent can start before there is a network to use.
	mkdir -p "$initdir/etc/cmdline.d"
	echo "rd.neednet=1" > "$initdir/etc/cmdline.d/95unlocker.conf"

	# The initqueue is the only hook point that runs alongside the password
	# prompt.
	#
	# pre-mount looks like the obvious counterpart to initramfs-tools' local-top,
	# and it is wrong: dracut-pre-mount.service is "After=cryptsetup.target", so
	# it runs only once the volume is already unlocked and the agent could never
	# answer the prompt it exists to answer. dracut-initqueue.service has no such
	# ordering, and "settled" runs once udev has settled, concurrently with
	# systemd-cryptsetup asking.
	#
	# 99 puts this after 99-networkd-run.sh, which is what starts the network.
	inst_hook initqueue/settled 99 "$moddir/unlocker-start.sh"

	# dracut-initqueue.service is conditional on this marker. The network module
	# creates it too, but relying on that would make the unlock depend on which
	# network module happened to be pulled in.
	mkdir -p "$initdir/lib/dracut"
	: > "$initdir/lib/dracut/need-initqueue"

	# pre-pivot corresponds to local-bottom. The agent is an endless loop and must
	# not survive the switch to the real root.
	inst_hook pre-pivot 05 "$moddir/unlocker-stop.sh"
}
