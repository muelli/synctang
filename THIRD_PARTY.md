# Third-party licences

Living document. Every dependency added to `go.mod`, `android/`, or vendored
from another repository is recorded here with how its licence was checked,
before the commit that adds it lands.

| Dependency | Licence | Compatible with AGPL-3.0-or-later | How checked |
|---|---|---|---|
| github.com/muelli/syncthing-socket | AGPL-3.0 | Yes, combining AGPLv3-only code into an AGPL-3.0-or-later work is permitted; the combined work is distributed under plain AGPLv3 terms `[V]` | Cloned the repository at `/tmp/syncthing-socket` on 2026-09-18 and read `LICENSE` in full: canonical GNU AGPL v3 text, no "or later" grant in the file itself. |

## Pending

- Full transitive dependency audit of `syncthing-socket`'s `go.mod` (pion/webrtc,
  syncthing/syncthing, hashicorp/yamux, and others). These are pulled in only
  through the library import, not vendored or copied, so their licences govern
  linking, not copying; each will be checked and added here as WP2 (transport)
  lands and actually imports the package. `[U]`
- Android/Kotlin and Gradle plugin dependencies, added as WP5 starts.
