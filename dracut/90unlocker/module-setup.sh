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

# unlocker_path finds the agent binary even when it is not on the PATH this
# script happens to run with. A kernel upgrade rebuilds the initrd from a dpkg
# maintainer script, and dpkg runs those with a PATH that does not include
# /usr/local/bin. A plain "require_binaries unlocker" therefore fails there,
# check() returns 1, dracut silently drops this whole module, and the machine
# comes up with an initrd that has no agent in it at all: remote unlock stops
# working after an unattended kernel upgrade, with nothing to show for it but a
# console prompt nobody is standing in front of. Observed exactly that on the
# test VM, whose 7.0.0-31 initrd contained no unlocker files whatsoever.
unlocker_path() {
	command -v unlocker 2>/dev/null && return 0
	for candidate in /usr/local/sbin/unlocker /usr/local/bin/unlocker \
		/usr/sbin/unlocker /usr/bin/unlocker; do
		if [ -x "$candidate" ]; then
			echo "$candidate"
			return 0
		fi
	done
	return 1
}

# unlocker_ca_bundles prints the CA bundles that exist, under the optional root
# given as $1 (empty for the real system; the test points it at a temporary
# tree). These are the paths Go's crypto/x509 looks the system trust store up
# at, which differ between distributions: Debian and Ubuntu have
# ca-certificates.crt, Fedora and RHEL have ca-bundle.crt (and a symlink at the
# Debian name is not there), openSUSE and Alpine have their own. A bundle at
# only one of them was all this module knew, so a Fedora initrd carried no
# trust store and the agent could not verify the relay pool or discovery.
unlocker_ca_bundles() {
	local root=${1:-} candidate
	for candidate in \
		/etc/ssl/certs/ca-certificates.crt \
		/etc/pki/tls/certs/ca-bundle.crt \
		/etc/ssl/ca-bundle.pem \
		/etc/pki/tls/cacert.pem \
		/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
		/etc/ssl/cert.pem; do
		# -r follows symlinks, so a dangling one is not reported.
		[ -r "$root$candidate" ] && echo "$candidate"
	done
	return 0
}

# synctang_network_conf prints /etc/synctang/network.conf if there is one, under
# the optional root given as $1 (as for unlocker_ca_bundles). It is the file that
# names a private relay pool and discovery server, and the agent in the initrd has
# no other place to read them from. -r follows symlinks, so a dangling one is not
# reported, which inst_simple would turn into a failed initrd build.
synctang_network_conf() {
	local root=${1:-} conf=/etc/synctang/network.conf
	[ -r "$root$conf" ] && echo "$conf"
	return 0
}

check() {
	unlocker_path > /dev/null || return 1
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
	inst_binary "$(unlocker_path)" /usr/local/bin/unlocker

	# 70crypt installs the cryptsetup binary only on its non-systemd branch: with
	# systemd present it installs dracut-crypt-generator and relies on
	# systemd-cryptsetup instead. The agent reads the LUKS2 header itself (to find
	# its own mr-1 token) and verifies a recovered secret before answering, so
	# without this the binary it needs is simply absent.
	inst_multiple cryptsetup

	# Discovery and the relay pool are reached over HTTPS, so the trust store has
	# to come along.
	local bundle have_bundle=
	for bundle in $(unlocker_ca_bundles); do
		inst_simple "$bundle"
		have_bundle=yes
	done
	if [ -z "$have_bundle" ]; then
		dwarn "unlocker: no CA bundle found (looked for ca-certificates.crt, ca-bundle.crt and"
		dwarn "unlocker: the other usual names). Install ca-certificates, or discovery will fail at boot."
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

	# The owner's choice of relay pool and discovery server, if they made one.
	# The kernel arguments rd.synctang.relay= and rd.synctang.discovery= override
	# it and need nothing installed: the agent reads /proc/cmdline itself. Absent
	# means the public Syncthing servers, so there is nothing to copy.
	local network_conf
	network_conf=$(synctang_network_conf)
	if [ -n "$network_conf" ]; then
		inst_simple "$network_conf"
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
