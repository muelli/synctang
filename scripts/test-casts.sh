#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests scripts/record-session.py and scripts/render-casts.py on small programs whose output is known.
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

# A program at a terminal: it sees a tty of the size asked for, and colour and UTF-8 are on.
# shellcheck disable=SC2016  # the program is meant to expand them, not this script
"$here/record-session.py" --out "$dir/tty.cast" --cols 90 --rows 20 --title 'a terminal' -- \
	bash -c 'test -t 1 && echo "at-a-tty"; stty size; echo "$TERM $LANG"; printf "\033[32m✓\033[0m green\n"' > /dev/null
has "$dir/tty.cast" '"width": 90' "the cast says how big the terminal was"
has "$dir/tty.cast" '"title": "a terminal"' "the cast has its title"
has "$dir/tty.cast" '20 90' "the program saw the size"
has "$dir/tty.cast" 'xterm-256color' "the program saw a colour terminal"
has "$dir/tty.cast" 'UTF-8' "the program saw a UTF-8 locale"
has "$dir/tty.cast" 'at-a-tty' "the program was at a terminal"

# An answer is typed once the output matches, and the command line is not recorded.
# shellcheck disable=SC2016
"$here/record-session.py" --out "$dir/ask.cast" --answer 'Continue\? \[y/N\]=y\n' -- \
	bash -c 'read -p "Continue? [y/N] " a; echo "you said $a"; exit 3' > /dev/null && code=0 || code=$?
if [ "$code" = 3 ]; then ok "the command's exit status is the recorder's"; else bad "the command's exit status is the recorder's ($code)"; fi
has "$dir/ask.cast" 'you said y' "the answer was typed"
lacks "$dir/ask.cast" 'read -p' "the command line is not in the cast"

# A command that never ends is stopped.
"$here/record-session.py" --out "$dir/slow.cast" --timeout 1 -- sleep 30 2> /dev/null && code=0 || code=$?
if [ "$code" = 124 ]; then ok "a command that does not end is stopped"; else bad "a command that does not end is stopped ($code)"; fi

# The page shows what the screen ended up with: a spinner that was cleared is gone, colours are kept.
"$here/record-session.py" --out "$dir/spin.cast" --title 'spinner' -- \
	bash -c 'printf "\r| working\033[K"; sleep 0.2; printf "\r/ working\033[K"; sleep 0.2; printf "\r\033[K"; printf "\033[1mdone\033[0m \033[31mred\033[0m\n"' > /dev/null
"$here/render-casts.py" "$dir" > /dev/null
page="$dir/index.html"
has "$page" 'a terminal' "the page has each session's title"
has "$page" 'color:#57ab5a">✓' "the page keeps the colours"
spinner="$dir/spinner-section.html"
sed -n '/<h2>spinner<\/h2>/,/<\/section>/p' "$page" > "$spinner"
lacks "$spinner" 'working' "a cleared spinner is not on the page"
has "$page" 'font-weight:bold">done' "the page keeps bold"
has "$page" 'asciinema play ask.cast' "the page says how to play a cast"
lacks "$page" '<script' "the page needs no JavaScript"

# What a session ended up showing, as plain text (also while it is still being written).
text="$("$here/render-casts.py" --text "$dir/spin.cast")"
if [ "$text" = "done red" ]; then ok "the final screen as plain text"; else bad "the final screen as plain text ($text)"; fi
head -n 1 "$dir/ask.cast" > "$dir/partial.cast"
printf '[0.1, "o", "whole"]\n[0.2, "o", "not wh' >> "$dir/partial.cast"
if "$here/render-casts.py" --text "$dir/partial.cast" > /dev/null 2>&1; then ok "a cast still being written can be read"; else bad "a cast still being written can be read"; fi

exit "$failed"
