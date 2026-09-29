<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Releasing

How the packages and the F-Droid repository are built, signed and published.
None of it is needed to install or use synctang.

## Publishing the F-Droid repository

Publishing is automatic: a push to `main` publishes a release-candidate
build, and a `v<versionCode>` tag publishes a release. Both need the two
signing secrets described below.

The repository is generated in CI and deployed to GitHub Pages as an
artifact (`configure-pages`, `upload-pages-artifact`, `deploy-pages`),
so no APK is ever committed to this repository. Earlier APKs are carried
forward by reading the live `index-v2.json`. Before the upload, the
workflow removes the fdroidserver keystore and `config.yml` from the
site directory and refuses to publish if anything that looks like private
key material is still there.

The version code is the commit count, so the changelog file under
`android/fastlane/metadata/android/en-US/changelogs/` has to be named
after the code that will actually be published; it drifts by one with
every push, which is why release notes are best finalised in the commit
that is tagged.

### Setting up publishing (once)

1. Generate two seeds and store them as repository secrets
   `APP_SIGNING_SEED` and `FDROID_SIGNING_SEED`:

       openssl rand -base64 48

   Back them up somewhere durable. The seeds *are* the signing keys: lose
   them and the app cannot be updated, only reinstalled from scratch.
2. Run the `bootstrap-signing` workflow once. It derives both identities
   inside CI, so the seeds never leave the secret store, and pushes the
   two public certificates to a `signing-bootstrap` branch (it also tries
   to open a pull request, which a repository setting may refuse; the
   branch is pushed either way).
3. Merge that branch into `main`, so the certificates land under
   `signing/`, and delete it. The certificates are public material; the
   signature does not reproduce byte for byte across runs (ECDSA is
   randomised), so the committed certificate is what an installed app
   is checked against.
4. Push to `main`. `publish-fdroid` builds, signs, generates the
   repository and deploys it. Fill in `AllowedAPKSigningKeys` and the
   version fields in `fdroid/com.github.muelli.synctang.yml` before
   submitting to f-droid.org; that file is for the f-droid.org catalogue
   and is not read by the self-hosted repository.

Until the secrets exist the publish workflow skips itself with a warning
rather than failing, so it is harmless to merge before step 1.

### Verifying a release

The published APK and the repository index (`entry.jar`) are recorded as
build-provenance attestations in the public Sigstore transparency log.
Download either file and run:

    gh attestation verify <file> --repo muelli/synctang

Attesting the index is what makes a split-view repository, serving a
different index to different people, detectable. The APK's signing
certificate is committed as `signing/app-cert.pem`.

## Debian package reproducibility

Building the Debian packages (`just deb`) from the same git commit
produces byte-identical `.deb` files, verified rather than assumed: `just
deb-repro` builds twice, deliberately varying umask, `TZ` and `LC_ALL`
between the two builds, and compares the results with `sha256sum`. The
`build-deb` CI job runs this same check on every push and pull request.

This holds for a given commit under: the exact Go toolchain patch version,
which `go.mod`'s own `go` directive decides rather than anything in CI (by
default Go downloads and uses whatever version `go.mod` asks for, whatever
is installed, so `GOTOOLCHAIN=local` in the `build-deb` job turns a
mismatch into a build failure instead of a silent substitution), and the
same `debhelper`/`dpkg` versions doing the packaging. A different Go patch
release is not verified to produce the same binary. It has not been checked
across different machines, architectures or Debian/Ubuntu releases;
"reproducible" here means "the same commit, rebuilt on hosts with matching
toolchain and packaging tool versions, matches", not "reproducible by
anyone, on anything, forever."

## Software bills of materials

CI generates a CycloneDX 1.6 SBOM for every compiled artefact, uploads
them as build artifacts, and (on a push to `main`) records them as
signed attestations against the artefact they describe, alongside the
build-provenance attestation that was already there. `gh attestation
verify` will show both.

| Artefact | SBOM | Where the list comes from |
|---|---|---|
| `synctang-unlocker_*.deb` | `unlocker.cdx.json` | Go build info in the shipped binary |
| `synctang-keyholder_*.deb` | `keyholder.cdx.json` | Go build info in the shipped binary |
| the APK | `app-go.cdx.json` | Go build info in the `libgojni.so` gomobile built |
| the APK | `app-jvm.cdx.json` | Gradle's resolved runtime classpath |

`scripts/generate-sbom.sh <artefact> <output.cdx.json>` does the Go
side and runs locally too; it accepts a `.deb`, an `.apk`, or a plain
executable. The APK needs two documents because its dependencies come
from two ecosystems and no single tool sees both: syft pointed at an
APK reports exactly one component, the APK itself, since nothing can
recover Maven coordinates from dex bytecode. Keeping them as two
documents rather than merging them is deliberate, so it stays visible
which evidence came from where.

Everything is read out of the built artefact rather than from `go.mod`
or `build.gradle.kts`, so an SBOM describes what was actually linked
rather than what was declared. That distinction is not academic here:
`THIRD_PARTY.md` records that `pion/ice` and `pion/stun` really are
linked into every Go binary, arriving with `syncthing-socket`'s root
package, even though no ICE code path is ever reached. A
manifest-derived SBOM would disagree with that entry.

### Why the binary, and not the source

Reading a binary trusts one tool's parse of one file, so `build-deb`
also asks the compiler the same question independently
(`scripts/verify-sbom-against-source.py`) and requires the two answers
to agree exactly. A misparse, a binary stripped of its build info, or
an SBOM generated against the wrong artefact would otherwise be a
confidently wrong document rather than a visible failure. Measured on
`cmd/unlocker`: 65 third-party modules from the binary, the same 65
from `go list -deps`.

Where the two disagree, the binary has so far been the more accurate,
which is why it is the one published:

- **It resolves replaced modules.** `syncthing-socket` declares a bare
  `module syncthing-socket`, so source analysis names a component no
  purl can resolve to a real project; the binary records
  `github.com/muelli/syncthing-socket` at a real pseudo-version.
- **It includes the standard library and toolchain**, as
  `stdlib@go1.26.0`, so a Go release CVE is in scope. `go list -deps`
  does not report the standard library as a module.
- **It sees generated code.** The APK's Go half contains `gobind` and
  `golang.org/x/mobile`, which `go list -deps ./mobile` does not report,
  because gomobile synthesises the binding layer at build time and it
  exists in no source tree here. A source-derived SBOM of the app would
  silently omit both. This is also why the cross-check covers the two
  Go binaries and not the APK: there, source and binary are *supposed*
  to differ.

The one thing a binary genuinely cannot supply is licence text, since it
carries no `LICENSE` files. Syft is therefore pointed at the local
module cache, reading each module's licence from the source it was built
from, which takes the Go SBOMs from 1 component in 67 carrying a licence
to 65. The two without are this repo's own module and, again,
`syncthing-socket`, whose declared path the cache lookup cannot resolve;
both are recorded in `THIRD_PARTY.md`.

The dracut package has no SBOM: it ships shell scripts and a systemd
unit, with nothing compiled in it.

Released APKs get theirs published next to the app itself, linked from
the F-Droid repository's landing page and reachable at
`sbom-go.cdx.json` and `sbom-jvm.cdx.json` (with per-version copies
named after the versionCode). Only the release being published gets
them, because no tool can recover a Maven dependency graph from an APK
that was built months ago, and a per-version file covering only half
the app would be worse than an honest single pair.
