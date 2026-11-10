#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests scripts/record.sh, scripts/answer.exp and scripts/render-casts.py on small programs whose output
# is known. The recording needs asciinema and expect; without them those tests are skipped, and say so.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
failed=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; failed=1; }

# has FILE FIXED-TEXT NAME: the file contains the text. lacks: it does not.
has() { if grep -qF -- "$2" "$1"; then ok "$3"; else bad "$3"; fi; }
lacks() { if grep -qF -- "$2" "$1"; then bad "$3"; else ok "$3"; fi; }

if command -v asciinema > /dev/null && command -v expect > /dev/null; then
	# A program at a terminal: it sees a tty of the size asked for, with colour and UTF-8 on.
	# shellcheck disable=SC2016  # the program is meant to expand them, not this script
	"$here/record.sh" "$dir/tty.cast" 90 'a terminal' -- \
		bash -c 'test -t 1 && echo "at-a-tty"; stty size; echo "$TERM $LANG"; printf "\033[32m✓\033[0m green\n"'
	has "$dir/tty.cast" '"width": 90' "the cast says how big the terminal was"
	has "$dir/tty.cast" '"title": "a terminal"' "the cast has its title"
	has "$dir/tty.cast" '45 90' "the program saw the size"
	has "$dir/tty.cast" 'xterm-256color C.UTF-8' "the program saw a UTF-8 colour terminal"
	has "$dir/tty.cast" 'at-a-tty' "the program was at a terminal"

	# An answer is typed once the output matches, the command's exit status is the recorder's, and the
	# command line is not in the cast.
	# shellcheck disable=SC2016
	"$here/record.sh" "$dir/ask.cast" 80 ask --answer 'Continue\? \[y/N\]' 'y\n' -- \
		bash -c 'read -p "Continue? [y/N] " a; echo "you said $a"; exit 3' && code=0 || code=$?
	if [ "$code" = 3 ]; then ok "the command's exit status is the recorder's"; else bad "the command's exit status is the recorder's ($code)"; fi
	has "$dir/ask.cast" 'you said y' "the answer was typed"
	lacks "$dir/ask.cast" 'read -p' "the command line is not in the cast"

	# Two answers, in order.
	# shellcheck disable=SC2016
	"$here/record.sh" "$dir/two.cast" 80 two --answer 'first\? ' 'one\n' --answer 'second\? ' 'two\n' -- \
		bash -c 'read -p "first? " a; read -p "second? " b; echo "got $a and $b"' || true
	has "$dir/two.cast" 'got one and two' "two answers are typed in order"

	# A command that ends before the question appears is an error, not a hang.
	"$here/record.sh" "$dir/early.cast" 80 early --answer 'never shown' 'x\n' -- true 2> /dev/null && code=0 || code=$?
	if [ "$code" = 3 ]; then ok "a command that ends before its question is an error"; else bad "a command that ends before its question is an error ($code)"; fi
else
	echo "skip the recording tests: asciinema and expect are needed"
fi

# The page shows what the screen ended up with: a spinner that was cleared is gone, colours are kept.
cast() { # NAME TITLE: a cast of a spinner, then a result in colour, made by hand
	printf '%s\n' "{\"version\": 2, \"width\": 80, \"height\": 24, \"title\": \"$2\"}" \
		'[0.1, "o", "\r| working\u001b[K"]' '[0.3, "o", "\r/ working\u001b[K"]' '[0.5, "o", "\r\u001b[K"]' \
		'[0.6, "o", "\u001b[1mdone\u001b[0m \u001b[31mred\u001b[0m \u001b[32m✓\u001b[0m\r\n"]' > "$dir/$1.cast"
}
cast spin 'spinner'
cast other 'another session'
"$here/render-casts.py" "$dir" > /dev/null
page="$dir/index.html"
has "$page" 'spinner' "the page has each session's title"
has "$page" 'another session' "the page has every session"
has "$page" 'color:#57ab5a">✓' "the page keeps the colours"
has "$page" 'font-weight:bold">done' "the page keeps bold"
section="$dir/section.html"
sed -n '/<h2>spinner<\/h2>/,/<\/section>/p' "$page" > "$section"
lacks "$section" 'working' "a cleared spinner is not on the page"
has "$page" 'asciinema play spin.cast' "the page says how to play a cast"
lacks "$page" '<script' "the page needs no JavaScript"

# What a session ended up showing, as plain text (also while it is still being written).
text="$("$here/render-casts.py" --text "$dir/spin.cast")"
if [ "$text" = "done red ✓" ]; then ok "the final screen as plain text"; else bad "the final screen as plain text ($text)"; fi
head -n 1 "$dir/spin.cast" > "$dir/partial.cast"
printf '[0.1, "o", "whole"]\n[0.2, "o", "not wh' >> "$dir/partial.cast"
if "$here/render-casts.py" --text "$dir/partial.cast" > /dev/null 2>&1; then ok "a cast still being written can be read"; else bad "a cast still being written can be read"; fi

exit "$failed"
