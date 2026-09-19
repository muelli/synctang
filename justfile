# SPDX-License-Identifier: AGPL-3.0-or-later

# Run everything CI runs, except the privileged loopback-LUKS tests and Android.
test: test-go lint em-dash-check

test-go:
	go test ./...

# Loopback LUKS tests exercise real dm-crypt via a truncated file image.
# They need CAP_SYS_ADMIN, so they run in a separate privileged job (or on the VM).
test-go-privileged:
	go test -tags privileged ./...

lint:
	find dracut scripts -name '*.sh' -print0 | xargs -0 -r shellcheck

em-dash-check:
	./scripts/check-em-dash.sh

install-dracut:
	sudo install -d /usr/lib/dracut/modules.d/90unlocker
	sudo install -m 0755 dracut/90unlocker/*.sh /usr/lib/dracut/modules.d/90unlocker/

# Build the three .debs (synctang-unlocker, synctang-keyholder, synctang-dracut).
#
# debian/changelog is generated here, not committed: the version is derived
# from the commit count rather than hand-maintained, so it always moves
# forward and never needs editing as part of an unrelated change.
#
# The trailer date is the commit's own date (git log -1 --format=%cD, already
# RFC 2822), not $(date -R): dpkg-buildpackage derives SOURCE_DATE_EPOCH from
# this date, and that epoch is stamped into every timestamp inside the .deb.
# A wall-clock date would make every build of the same commit differ; the
# commit date is fixed, so builds of the same commit are reproducible.
#
# "-d" on dpkg-buildpackage is required: debian/control's Build-Depends
# names no Go toolchain (there is no Debian package for the exact Go
# version this project requires), so dpkg-checkbuilddeps would otherwise
# refuse to build on a machine that has one installed perfectly well.
deb:
	echo "synctang (0.0.$(git rev-list --count HEAD)-1) unstable; urgency=medium" > debian/changelog
	echo "" >> debian/changelog
	echo "  * Packaged build, see git log for actual changes." >> debian/changelog
	echo "" >> debian/changelog
	echo " -- synctang CI <noreply@github.com>  $(git log -1 --format=%cD)" >> debian/changelog
	dpkg-buildpackage -us -uc -b -d
	mkdir -p dist
	mv -f ../*.deb dist/
	echo "built:"
	ls -1 dist/*.deb

# Build the packages twice, deliberately varying the environment between
# the two builds (umask, TZ, LC_ALL), and check the resulting .debs are
# byte-identical. This is what actually verifies reproducibility rather
# than assuming it: SOURCE_DATE_EPOCH alone is not enough proof, since a
# build could still leak the umask, locale or timezone it happened to run
# under into file metadata, sort order or rendered text.
deb-repro:
	#!/usr/bin/env bash
	set -euo pipefail
	echo "Go toolchain: $(go version)"
	rm -rf dist dist-a dist-b
	umask 022
	TZ=UTC LC_ALL=C just deb
	mv dist dist-a
	umask 077
	TZ=Pacific/Kiritimati LC_ALL=C.UTF-8 just deb
	mv dist dist-b
	rm -rf debian/synctang-unlocker debian/synctang-keyholder debian/synctang-dracut debian/tmp debian/.debhelper debian/debhelper-build-stamp debian/files -- *.substvars 2>/dev/null || true
	status=0
	for a in dist-a/*.deb; do
		b="dist-b/$(basename "$a")"
		if [ ! -f "$b" ]; then
			echo "MISSING: $b was not produced by the second build" >&2
			status=1
			continue
		fi
		sum_a=$(sha256sum "$a" | cut -d' ' -f1)
		sum_b=$(sha256sum "$b" | cut -d' ' -f1)
		if [ "$sum_a" = "$sum_b" ]; then
			echo "OK  $(basename "$a")  $sum_a"
		else
			echo "MISMATCH  $(basename "$a")" >&2
			echo "  a: $sum_a" >&2
			echo "  b: $sum_b" >&2
			if command -v diffoscope >/dev/null 2>&1; then
				diffoscope "$a" "$b" || true
			else
				echo "diffoscope not installed; falling back to dpkg-deb -c / ar t diffs" >&2
				diff -u <(ar t "$a") <(ar t "$b") || true
				diff -u <(dpkg-deb -c "$a") <(dpkg-deb -c "$b") || true
			fi
			status=1
		fi
	done
	if [ "$status" -eq 0 ]; then
		echo "reproducible: both builds byte-identical"
	fi
	exit "$status"
