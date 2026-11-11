#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The text a release shows on its GitHub page, as Markdown on standard output.
#
#   scripts/release-notes.sh <tag> <owner/repo> <changelog file> <release directory>
#
# The changelog file is the phone app's release note for this version
# (android/fastlane/metadata/android/en-US/changelogs/<versionCode>.txt): what a person using the
# app will notice. The directory is the one the release is made from, so that the list of files is
# the list of files that are attached. Missing or empty changelog stops the release: the release
# process (docs/releasing.md) says to write it first, and a release without it is a mistake.
set -euo pipefail

if [ "$#" -ne 4 ]; then
	echo "usage: $0 <tag> <owner/repo> <changelog file> <release directory>" >&2
	exit 2
fi
tag=$1 repo=$2 changelog=$3 dir=$4
owner=$(tr '[:upper:]' '[:lower:]' <<< "${repo%%/*}")
name=${repo#*/}

if [ ! -s "$changelog" ]; then
	echo "release-notes: $changelog is missing or empty: write the release note for the phone app first (docs/releasing.md)" >&2
	exit 1
fi

# The files in the release directory, by name, one to a line.
files() { find "$dir" -maxdepth 1 -type f -printf '%f\n' | sort; }
# The first file whose name matches a pattern, or nothing.
pick() { files | grep -E -- "$1" | head -1 || true; }

cat << EOF
## synctang $tag

synctang unlocks an encrypted disk (LUKS) when a person approves.
A key holder, a phone or a laptop, answers the machine's request.

### The phone app

EOF
cat "$changelog"

cat << EOF

### Install

On Debian and Ubuntu. Use the arm64 files on an arm64 machine.

EOF
unlocker=$(pick '^synctang-unlocker_.*_amd64\.deb$')
dracut=$(pick '^synctang-dracut_.*_all\.deb$')
keyholder=$(pick '^synctang-keyholder_.*_amd64\.deb$')
if [ -n "$unlocker" ] && [ -n "$dracut" ]; then
	printf 'The machine with the encrypted disk:\n\n    sudo apt install ./%s ./%s\n\n' "$unlocker" "$dracut"
fi
if [ -n "$keyholder" ]; then
	printf 'The laptop:\n\n    sudo apt install ./%s\n\n' "$keyholder"
fi
cat << EOF
The phone: add the F-Droid repository \`https://$owner.github.io/$name/fdroid/repo\` and install "synctang key holder".

Then pair a key holder with the machine: the README and \`docs/usage.md\` show how.

### Verify

Every file below has a build-provenance attestation and a cosign bundle (\`<file>.sigstore.json\`).
Check the list of checksums first, then the files against it:

    sha256sum --check --ignore-missing SHA256SUMS

Check one file against GitHub's attestation:

    gh attestation verify <file> --repo $repo

Or check its cosign bundle, which needs no GitHub account:

    cosign verify-blob --bundle <file>.sigstore.json \\
      --certificate-identity-regexp '^https://github.com/$repo/\\.github/workflows/ci\\.yml@refs/tags/v[0-9]+\$' \\
      --certificate-oidc-issuer https://token.actions.githubusercontent.com <file>

### Files

| File | What it is |
| --- | --- |
EOF
while IFS= read -r f; do
	case "$f" in
	synctang-unlocker_*.deb) what='the machine side: unlocks the disk at boot, pairs and enrols key holders' ;;
	synctang-dracut_*.deb) what='the dracut module that starts the unlocker in the initrd' ;;
	synctang-keyholder_*.deb) what='the laptop key holder' ;;
	*.cdx.json) what='the software bill of materials of the package (CycloneDX)' ;;
	SHA256SUMS) what='the checksum of every file here' ;;
	*.sigstore.json) continue ;;
	*) what='' ;;
	esac
	bundle=''
	[ -e "$dir/$f.sigstore.json" ] || bundle=' (no bundle)'
	printf '| %s | %s%s |\n' "\`$f\`" "$what" "$bundle"
done < <(files)
cat << 'EOF'

The manual pages are in the packages: `man synctang-unlocker`, `man synctang-keyholder`. `docs/usage.md` has the long form.
EOF
