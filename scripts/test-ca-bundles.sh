#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests which CA bundles dracut/90unlocker/module-setup.sh would put in the
# initrd. Discovery and the relay pool are reached over HTTPS, and Go looks the
# trust store up at a few fixed paths that differ between distributions: Debian
# and Ubuntu use /etc/ssl/certs/ca-certificates.crt, Fedora and RHEL use
# /etc/pki/tls/certs/ca-bundle.crt. A module that knows only the first leaves a
# Fedora machine with an initrd that cannot verify the relay, which was found by
# booting one: the agent started, and no key holder could reach it.
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
. "$here/../dracut/90unlocker/module-setup.sh"

check() {
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

root() { rm -rf "${work:?}/root"; mkdir -p "$work/root"; }
put() { mkdir -p "$work/root/$(dirname "$1")"; : > "$work/root/$1"; }
found() { unlocker_ca_bundles "$work/root" | grep -qx "$1"; }
nothing_found() { [ -z "$(unlocker_ca_bundles "$work/root")" ]; }

root
put etc/ssl/certs/ca-certificates.crt
check "Debian and Ubuntu: /etc/ssl/certs/ca-certificates.crt" found /etc/ssl/certs/ca-certificates.crt

root
put etc/pki/tls/certs/ca-bundle.crt
check "Fedora and RHEL: /etc/pki/tls/certs/ca-bundle.crt" found /etc/pki/tls/certs/ca-bundle.crt

root
put etc/ssl/ca-bundle.pem
check "openSUSE: /etc/ssl/ca-bundle.pem" found /etc/ssl/ca-bundle.pem

root
put etc/ssl/cert.pem
check "Alpine and others: /etc/ssl/cert.pem" found /etc/ssl/cert.pem

root
put etc/ssl/certs/ca-certificates.crt
put etc/pki/tls/certs/ca-bundle.crt
check "both present: both are carried" bash -c "$(declare -f unlocker_ca_bundles found); work='$work' found /etc/ssl/certs/ca-certificates.crt && work='$work' found /etc/pki/tls/certs/ca-bundle.crt"

root
check "none present: nothing is claimed" nothing_found

# A symlink that points nowhere is not a bundle.
root
mkdir -p "$work/root/etc/pki/tls/certs"
ln -s /nonexistent "$work/root/etc/pki/tls/certs/ca-bundle.crt"
check "a dangling symlink is not a bundle" nothing_found

exit "$fail"
