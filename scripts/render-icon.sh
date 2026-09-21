#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Tobias Mueller and synctang contributors
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Regenerate the raster icon(s) from the editable SVG source (artwork/icon.svg).
# Run after editing the SVG. Requires rsvg-convert (package: librsvg2-bin).
set -euo pipefail
cd "$(dirname "$0")/.."

SRC=android/artwork/icon.svg
OUT=android/fastlane/metadata/android/en-US/images/icon.png

command -v rsvg-convert >/dev/null || { echo "rsvg-convert not found (install librsvg2-bin)"; exit 1; }
rsvg-convert -w 512 -h 512 "$SRC" -o "$OUT"
echo "wrote $OUT ($(stat -c%s "$OUT") bytes)"
echo "Reminder: android/app/src/main/res/drawable/ic_launcher_foreground.xml carries the"
echo "same shapes for the adaptive launcher icon — update it too if you changed them."
