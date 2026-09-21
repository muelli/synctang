#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Write a CycloneDX software bill of materials for one built artefact.
#
#   scripts/generate-sbom.sh <artefact> <output.cdx.json>
#
# The artefact may be a Debian package, an APK, or a plain executable.
#
# Everything here reads the *built* artefact rather than go.mod, so the
# result describes what was actually linked into the thing being
# shipped. That distinction matters for this project: THIRD_PARTY.md
# already records that pion/ice and pion/stun are genuinely linked in
# (they arrive with syncthing-socket's root package) even though no ICE
# code path is ever reached, and an SBOM generated from the manifest
# instead of the binary would disagree with that entry in one direction
# or the other.
#
# CycloneDX 1.6 throughout, because that is what the Gradle plugin
# emits for the Android app's JVM dependencies, and one format across
# all artefacts is easier to consume than two.
set -euo pipefail

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <artefact> <output.cdx.json>" >&2
	exit 2
fi

artefact=$1
output=$2

if ! command -v syft >/dev/null 2>&1; then
	echo "$0: syft is not installed; see THIRD_PARTY.md for what it is" >&2
	exit 1
fi

mkdir -p "$(dirname "$output")"

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

case "$artefact" in
*.deb)
	# syft reads a .deb as a single Debian package and does not look
	# inside it, so the Go module list would be missing entirely.
	# Unpacking first gives it the actual executable to read build
	# info out of.
	dpkg-deb -x "$artefact" "$scratch/root"
	target="dir:$scratch/root"
	;;
*.apk)
	# The Go half of the app. Android's JVM dependencies come from
	# Gradle instead (the cyclonedxBom task), because nothing can
	# recover them from dex bytecode: syft scanning an APK directly
	# reports one component, the APK itself.
	#
	# One ABI is enough. gomobile compiles every ABI from the same Go
	# sources against the same go.mod, so the module list does not
	# vary between them; only the machine code does.
	unzip -q -o "$artefact" 'lib/*/libgojni.so' -d "$scratch" 2>/dev/null || true
	so=$(find "$scratch" -name 'libgojni.so' | sort | head -1)
	if [ -z "$so" ]; then
		echo "$0: no libgojni.so in $artefact; is this a synctang APK?" >&2
		exit 1
	fi
	target="file:$so"
	;;
*)
	target="file:$artefact"
	;;
esac

# The component list comes from the artefact, but licences cannot: a
# compiled binary carries no LICENSE files. Pointing syft at the local
# module cache lets it read each module's licence from the source it was
# built from, which takes this project's Go SBOMs from 1 component in 67
# carrying a licence to 65. The module cache is populated by the build
# that produced the artefact, so this needs nothing fetched.
export SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true

syft scan "$target" -o "cyclonedx-json@1.6=$output" -q

# A syft run that reads the wrong file, or a binary stripped of its
# build info, still writes a valid SBOM: it just has no Go modules in
# it. That is worse than no SBOM at all, because it asserts that a
# binary linking dozens of dependencies has none, so check for the
# thing actually being looked for rather than for a non-empty file.
# Counting components alone is not enough: /bin/true produces two.
read -r components modules licensed <<<"$(python3 -c "
import json, sys
with open(sys.argv[1]) as f:
    components = json.load(f).get('components', [])
go = [c for c in components if str(c.get('purl', '')).startswith('pkg:golang/')]
print(len(components), len(go), len([c for c in go if c.get('licenses')]))
" "$output")"

if [ "$modules" -lt 2 ]; then
	echo "$0: $output has $modules Go modules in $components components, which cannot be right for $artefact" >&2
	exit 1
fi

# Zero licences means the module cache lookup above found nothing at all,
# usually because the SBOM is being generated somewhere the build did not
# happen. Worth failing on, where merely incomplete coverage is not: two
# modules here legitimately have none (this repo's own, and
# syncthing-socket, whose bare declared module path the cache lookup
# cannot resolve).
if [ "$licensed" -eq 0 ]; then
	echo "$0: $output has no licence data at all; was this run without the module cache the build used?" >&2
	exit 1
fi

echo "$output: $modules Go modules in $components components ($licensed with licences), from $artefact"
