#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests which of the owner's network settings dracut/90synctang/module-setup.sh
# would put in the initrd. The agent there reads /etc/synctang/network.conf, the
# file naming a private relay pool and discovery server, and has no other place to
# get it from; a module that left it out would boot a machine that announces
# itself on the public servers the owner had chosen not to use.
#
# shellcheck disable=SC2329
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

# The module's own functions, without running dracut: module-setup.sh only
# defines functions when sourced.
# shellcheck disable=SC1091
. "$here/../dracut/90synctang/module-setup.sh"

check() {
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

root() { rm -rf "${work:?}/root"; mkdir -p "$work/root"; }
put() { mkdir -p "$work/root/$(dirname "$1")"; printf '%s\n' "${2:-}" > "$work/root/$1"; }
carried() { [ "$(synctang_network_conf "$work/root")" = /etc/synctang/network.conf ]; }
nothing_carried() { [ -z "$(synctang_network_conf "$work/root")" ]; }

root
put etc/synctang/network.conf "discovery=https://disco.example.net/v2/"
check "a network.conf that is there is carried" carried

root
check "no network.conf: nothing is claimed" nothing_carried

root
put etc/unlocker/machine.pem
check "the machine identity alone is not a network.conf" nothing_carried

# A symlink that points nowhere is not a file to copy; inst_simple would fail
# the whole initrd build on it.
root
mkdir -p "$work/root/etc/synctang"
ln -s /nonexistent "$work/root/etc/synctang/network.conf"
check "a dangling symlink is not carried" nothing_carried

exit "$fail"
