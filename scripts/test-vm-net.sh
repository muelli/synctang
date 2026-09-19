#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Create (or tear down) a private bridge for the disposable test VM, so that the
# VM and this host share a real layer 2 segment.
#
# QEMU's default user-mode networking cannot carry multicast, which means local
# discovery cannot work under it at all and every test has to go out to the
# public relay pool and back. That is slow, depends on third-party
# infrastructure being healthy, and quietly turns unrelated relay flakiness into
# what looks like a bug in this project. A bridge costs one interface and makes
# local discovery the fast, dependency-free default it is meant to be.
#
# Deliberately isolated: no NAT to the outside world is set up, so a VM on this
# bridge has a LAN and no Internet. That is the condition A11 is about, and
# making it the default for local testing means the offline path is the one
# getting exercised rather than the one nobody tries until it breaks.

set -euo pipefail

BRIDGE="${BRIDGE:-sytbr0}"
TAP="${TAP:-sytap0}"
BRIDGE_ADDR="${BRIDGE_ADDR:-10.77.0.1/24}"
DHCP_RANGE="${DHCP_RANGE:-10.77.0.50,10.77.0.150,12h}"
TAP_OWNER="${TAP_OWNER:-$(id -un 1000 2>/dev/null || echo ubuntu)}"
PIDFILE="/run/synctang-test-dnsmasq.pid"

if [ "$(id -u)" -ne 0 ]; then
	echo "$0 must run as root" >&2
	exit 1
fi

case "${1:-up}" in
	up)
		if ! ip link show "$BRIDGE" > /dev/null 2>&1; then
			ip link add name "$BRIDGE" type bridge
			ip addr add "$BRIDGE_ADDR" dev "$BRIDGE"
			ip link set "$BRIDGE" up
		fi
		if ! ip link show "$TAP" > /dev/null 2>&1; then
			ip tuntap add dev "$TAP" mode tap user "$TAP_OWNER"
			ip link set "$TAP" master "$BRIDGE"
			ip link set "$TAP" up
		fi
		# The guest's initrd asks for DHCP (the dracut network module's default
		# is DHCP on every non-loopback interface), so without a server on this
		# bridge it would come up with no address and the agent would have no
		# network to listen on.
		if [ ! -f "$PIDFILE" ] || ! kill -0 "$(cat "$PIDFILE")" 2> /dev/null; then
			dnsmasq --interface="$BRIDGE" --bind-interfaces \
				--dhcp-range="$DHCP_RANGE" \
				--dhcp-authoritative \
				--except-interface=lo \
				--pid-file="$PIDFILE"
		fi
		echo "bridge $BRIDGE up at $BRIDGE_ADDR, tap $TAP owned by $TAP_OWNER"
		;;
	down)
		if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2> /dev/null; then
			kill "$(cat "$PIDFILE")"
			rm -f "$PIDFILE"
		fi
		ip link show "$TAP" > /dev/null 2>&1 && ip link del "$TAP"
		ip link show "$BRIDGE" > /dev/null 2>&1 && ip link del "$BRIDGE"
		echo "bridge $BRIDGE and tap $TAP removed"
		;;
	*)
		echo "usage: $0 [up|down]" >&2
		exit 1
		;;
esac
