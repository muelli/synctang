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
# "-d" on dpkg-buildpackage is required: debian/control's Build-Depends
# names no Go toolchain (there is no Debian package for the exact Go
# version this project requires), so dpkg-checkbuilddeps would otherwise
# refuse to build on a machine that has one installed perfectly well.
deb:
	echo "synctang (0.0.$(git rev-list --count HEAD)-1) unstable; urgency=medium" > debian/changelog
	echo "" >> debian/changelog
	echo "  * Packaged build, see git log for actual changes." >> debian/changelog
	echo "" >> debian/changelog
	echo " -- synctang CI <noreply@github.com>  $(date -R)" >> debian/changelog
	dpkg-buildpackage -us -uc -b -d
	mkdir -p dist
	mv -f ../*.deb dist/
	echo "built:"
	ls -1 dist/*.deb
