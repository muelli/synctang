#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs each fuzz target of mrcore for a short while, as CI does:
#
#   scripts/ci-fuzz.sh [FUZZTIME] TARGET...        (FUZZTIME defaults to 20s)
#
# Go's fuzzing engine sometimes ends a run that found nothing with "context deadline exceeded": the time
# limit came while it was working on an input, and it reports that as a failure of the target. It is not a
# finding (no input is written, nothing crashed), so such a run is repeated, up to three times. A real
# finding is never repeated: it prints "Failing input written to", or panics, or a worker dies, and the
# script stops with that status at once.
set -uo pipefail

fuzztime=20s
case "${1:-}" in
	[0-9]*s | [0-9]*m) fuzztime=$1; shift ;;
esac
[ $# -ge 1 ] || { echo "usage: ci-fuzz.sh [FUZZTIME] TARGET..." >&2; exit 2; }

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

for target in "$@"; do
	echo "== $target"
	for attempt in 1 2 3; do
		if go test ./mrcore/ -run "^$target\$" -fuzz "^$target\$" -fuzztime="$fuzztime" 2>&1 | tee "$out"; then
			continue 2
		fi
		if grep -q -e 'Failing input written to' -e '^panic:' -e 'fuzzing process hung or terminated' -e 'fuzzing process terminated unexpectedly' "$out"; then
			echo "ci-fuzz: $target found something (above); not repeating" >&2
			exit 1
		fi
		if grep -q 'context deadline exceeded' "$out"; then
			echo "ci-fuzz: $target hit the fuzz engine's own deadline (attempt $attempt of 3), which is not a finding" >&2
			continue
		fi
		echo "ci-fuzz: $target failed in a way this script does not know (above)" >&2
		exit 1
	done
	echo "ci-fuzz: $target hit the engine's deadline three times in a row" >&2
	exit 1
done
