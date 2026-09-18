#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build the gomobile .aar the Android app links against.
#
# This lives in a script rather than inline in build.gradle.kts so the
# exact same command runs locally, from Gradle and from CI, and so the
# flags have one home. Gradle's buildGoMobile task calls it.
#
# Usage: scripts/gomobile-bind.sh <output.aar> [target]
#   target defaults to every Android ABI; pass for example
#   "android/amd64" to build only what an x86_64 emulator needs, which
#   is several times quicker.
set -eu

out=${1:?usage: gomobile-bind.sh <output.aar> [target]}
target=${2:-android}

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

gomobile=${GOMOBILE:-}
if [ -z "$gomobile" ]; then
	if command -v gomobile >/dev/null 2>&1; then
		gomobile=gomobile
	elif [ -x "${HOME}/go/bin/gomobile" ]; then
		gomobile="${HOME}/go/bin/gomobile"
	else
		echo "gomobile not found: go install golang.org/x/mobile/cmd/gomobile@latest" >&2
		exit 1
	fi
fi

# gomobile shells out to gobind and finds it on PATH only, so the
# directory gomobile itself came from has to be on PATH too. Otherwise
# it fails with "gobind was not found" even when both are installed
# side by side.
gomobile_dir=$(dirname -- "$(command -v "$gomobile")")
PATH="${gomobile_dir}:${PATH}"
export PATH

mkdir -p "$(dirname -- "$out")"

# -androidapi 26 matches the app's minSdk. -checklinkname=0 is needed
# because the Syncthing libraries this transitively pulls in use
# //go:linkname against standard library internals, which the linker
# rejects by default from Go 1.23 onwards.
cd "$repo_root"
exec "$gomobile" bind \
	-target="$target" \
	-androidapi 26 \
	-trimpath \
	-ldflags="-s -w -checklinkname=0" \
	-o "$out" \
	github.com/muelli/synctang/mobile
