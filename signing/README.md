<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
# Signing certificates

This directory holds the **public** certificates of the seed-derived signing
identities, and nothing else. No private key or seed belongs here, or
anywhere else in the repository.

- `app-cert.pem` signs the APK. Android identifies an app by these exact
  certificate bytes, so replacing it forces every user to uninstall before
  they can update.
- `fdroid-cert.pem` signs the repository index. Its SHA-256 is the repo
  fingerprint that clients pin when the repo is added; replacing it means
  users must remove and re-add the repository, not merely rescan the QR code.

Both are produced once by the `bootstrap-signing` workflow from the
`APP_SIGNING_SEED` and `FDROID_SIGNING_SEED` secrets, which never leave the
GitHub secret store. `scripts/derive-signing-key.py` rebuilds the private
keys from those seeds on demand and refuses to run if a seed does not
reproduce the certificate committed here, so a wrong secret fails the build
rather than shipping an app nobody can update.
