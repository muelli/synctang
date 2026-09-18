#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Print the <licenses> block of every resolved POM in the Gradle cache
# whose coordinates match the arguments, so THIRD_PARTY.md rows record
# what the artefact itself declares rather than what a web page says.
#
# Usage: scripts/check-pom-licences.sh <group:artifact:version> ...
set -eu

cache="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"

for coord in "$@"; do
	group=$(echo "$coord" | cut -d: -f1)
	artifact=$(echo "$coord" | cut -d: -f2)
	version=$(echo "$coord" | cut -d: -f3)
	pom=$(find "$cache/$group/$artifact/$version" -name "*.pom" 2>/dev/null | head -1)
	if [ -z "$pom" ]; then
		echo "$coord: POM not in the cache"
		continue
	fi
	name=$(tr -d '\n' <"$pom" | grep -o '<licenses>.*</licenses>' | grep -o '<name>[^<]*</name>' | head -1)
	echo "$coord: ${name:-no <licenses> block}"
done
