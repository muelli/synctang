#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Records a command at a terminal, as an asciicast that "asciinema play" plays:
#
#   record.sh OUT.cast COLUMNS TITLE [--answer REGEX TEXT]... -- COMMAND [ARGS...]
#
# The command thinks it is at a UTF-8 colour terminal COLUMNS wide, so it colours and decorates its output
# as it does for a person. Each --answer is typed once, when the output matches REGEX (scripts/answer.exp
# does the typing; TEXT may hold \n for Enter). The exit status is the command's: asciinema's own is not.
# Needs asciinema and, for answers, expect.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
[ $# -ge 4 ] || { echo "usage: record.sh OUT.cast COLUMNS TITLE [--answer REGEX TEXT]... -- COMMAND..." >&2; exit 2; }
out=$1 columns=$2 title=$3
shift 3

answers=()
while [ "${1:-}" = --answer ]; do
	[ $# -ge 3 ] || { echo "record.sh: --answer wants a REGEX and a TEXT" >&2; exit 2; }
	answers+=(-answer "$2" "$3")
	shift 3
done
[ "${1:-}" = -- ] || { echo "record.sh: expected -- before the command" >&2; exit 2; }
shift
[ $# -ge 1 ] || { echo "record.sh: no command" >&2; exit 2; }

status="$(mktemp)"
trap 'rm -f "$status"' EXIT
if [ ${#answers[@]} -gt 0 ]; then
	command="$(printf '%q ' expect "$here/answer.exp" "${answers[@]}" -- "$@")"
else
	command="$(printf '%q ' "$@")"
fi

# The command line is not in the cast: asciinema records what the terminal showed, and the title.
env TERM=xterm-256color LANG=C.UTF-8 NO_COLOR= \
	asciinema rec --quiet --overwrite --cols "$columns" --rows 45 --title "$title" \
	-c "$command; echo \$? > $(printf '%q' "$status")" "$out" > /dev/null 2>&1
code="$(cat "$status" 2> /dev/null || true)"
exit "${code:-1}"
