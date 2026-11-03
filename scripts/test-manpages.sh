#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Lints the man pages in man/. Uses mandoc (-Tlint) if it is installed, and
# groff (all warnings, no output) otherwise; either way any warning at all is a
# failure, since the pages are plain man(7) and have no reason to produce one.
# The pages' completeness against the programs is a Go test, not this.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fail=0

if command -v mandoc > /dev/null 2>&1; then
	tool=mandoc
	lint() { mandoc -Tlint -Wall "$1" 2>&1; }
elif command -v groff > /dev/null 2>&1; then
	tool=groff
	lint() { groff -man -ww -z -wall "$1" 2>&1; }
else
	echo "neither mandoc nor groff is installed" >&2
	exit 1
fi
echo "linting with $tool"

for page in "$here"/../man/*.[1-8]; do
	out=$(lint "$page") || true
	if [ -n "$out" ]; then
		echo "FAIL $(basename "$page")"
		echo "$out"
		fail=1
	else
		echo "ok   $(basename "$page")"
	fi
done

# A page with no content would lint clean; there must be pages.
count=$(find "$here/../man" -maxdepth 1 -name '*.[1-8]' | wc -l)
if [ "$count" -lt 3 ]; then
	echo "FAIL expected at least three man pages, found $count"
	fail=1
fi

exit "$fail"
