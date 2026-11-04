#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Build the Syncthing project's relay and discovery servers, strelaysrv and
# stdiscosrv, into the directory given as $1. The integration tests and the
# end-to-end test (scripts/e2e-vm.sh) run a machine and a key holder through
# real ones, not fakes.
#
# They are built outside this module because their own module's replace
# directives stop "go install ...@version" from working, and their
# dependencies are not in this module's go.sum.
#
#   scripts/build-syncthing-servers.sh /tmp/stbin
#   STRELAYSRV=/tmp/stbin/strelaysrv STDISCOSRV=/tmp/stbin/stdiscosrv go test ./integration

set -euo pipefail

VERSION="${SYNCTHING_VERSION:-v1.30.0}"
OUT="${1:?usage: $0 OUTPUT-DIRECTORY}"

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cd "$WORK"
printf 'module stbuild\n\ngo 1.25\n\nrequire github.com/syncthing/syncthing %s\n' "$VERSION" > go.mod
GOFLAGS=-mod=mod go build -o "$OUT/" \
	github.com/syncthing/syncthing/cmd/strelaysrv github.com/syncthing/syncthing/cmd/stdiscosrv
echo "built $OUT/strelaysrv and $OUT/stdiscosrv ($VERSION)"
