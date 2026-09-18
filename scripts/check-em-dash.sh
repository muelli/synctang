#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
set -euo pipefail

# Fails the build if U+2014 (EM DASH) appears anywhere in tracked text files.
# Double hyphens ("--") are fine; they are not the em dash.

matches=$(git grep -In $'—' -- . ':!*.png' ':!*.jpg' ':!*.jks' ':!*.keystore' ':!scripts/check-em-dash.sh' || true)

if [ -n "$matches" ]; then
	echo "em dash (U+2014) found, replace with a full stop, semicolon, colon or parentheses:" >&2
	echo "$matches" >&2
	exit 1
fi

echo "no em dashes found"
