#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests scripts/release-notes.sh: the text a release shows on its GitHub page.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
failed=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; failed=1; }
has() { if grep -qF -- "$2" <<< "$notes"; then ok "$1"; else bad "$1 (missing: $2)"; fi; }
hasnt() { if grep -qiF -- "$2" <<< "$notes"; then bad "$1 (found: $2)"; else ok "$1"; fi; }

mkdir "$dir/release"
for f in synctang-unlocker_0.0.389-1_amd64.deb synctang-unlocker_0.0.389-1_arm64.deb \
	synctang-keyholder_0.0.389-1_amd64.deb synctang-keyholder_0.0.389-1_arm64.deb \
	synctang-dracut_0.0.389-1_all.deb synctang-unlocker-amd64.cdx.json SHA256SUMS; do
	: > "$dir/release/$f"
	: > "$dir/release/$f.sigstore.json"
done
rm "$dir/release/synctang-unlocker-amd64.cdx.json.sigstore.json" # a file without a bundle is said so
cat > "$dir/changelog.txt" <<'TXT'
Unlock is one tap: the fingerprint prompt comes up by itself.
- The status dot checks every minute.
- Keyboard shortcuts and screen reader labels throughout.
TXT

notes=$("$here/release-notes.sh" v389 muelli/synctang "$dir/changelog.txt" "$dir/release")

first=$(head -1 <<< "$notes")
if [ "$first" = "## synctang v389" ]; then ok "it starts with the name and the tag"; else bad "first line is: $first"; fi
has "the phone changes are there as they were written" "- The status dot checks every minute."
has "the phone changes come under a heading of their own" "### The phone app"
has "the install section" "### Install"
has "the machine is told what to install" "sudo apt install ./synctang-unlocker_0.0.389-1_amd64.deb ./synctang-dracut_0.0.389-1_all.deb"
has "the laptop is told what to install" "sudo apt install ./synctang-keyholder_0.0.389-1_amd64.deb"
has "the other architecture is named" "arm64"
has "the phone repository follows the repository's owner and name" "https://muelli.github.io/synctang/fdroid/repo"
has "the verify section" "### Verify"
has "the checksum command" "sha256sum --check --ignore-missing SHA256SUMS"
has "the attestation command names the repository" "gh attestation verify <file> --repo muelli/synctang"
has "the cosign command names this workflow on a tag" "--certificate-identity-regexp '^https://github.com/muelli/synctang/\\.github/workflows/ci\\.yml@refs/tags/v[0-9]+\$'"
has "the files section" "### Files"
has "each package is listed" "synctang-keyholder_0.0.389-1_arm64.deb"
has "the SBOM is listed" "synctang-unlocker-amd64.cdx.json"
has "a file without a bundle is said to have none" "no bundle"
has "the manual is linked" "docs/usage.md"
hasnt "no promise about compatibility, no list of what is missing" "compatib"
hasnt "no em dash" "$(printf '\342\200\224')"

if "$here/release-notes.sh" v389 muelli/synctang "$dir/missing.txt" "$dir/release" > /dev/null 2>&1; then
	bad "a missing changelog went through"
else
	ok "a missing changelog stops the release"
fi
: > "$dir/empty.txt"
if "$here/release-notes.sh" v389 muelli/synctang "$dir/empty.txt" "$dir/release" > /dev/null 2>&1; then
	bad "an empty changelog went through"
else
	ok "an empty changelog stops the release"
fi

exit "$failed"
