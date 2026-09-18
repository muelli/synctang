# Third-party licences

Living document. Every dependency added to `go.mod`, `android/`, or vendored
from another repository is recorded here with how its licence was checked,
before the commit that adds it lands.

| Dependency | Licence | Compatible with AGPL-3.0-or-later | How checked |
|---|---|---|---|
| github.com/muelli/syncthing-socket | AGPL-3.0 | Yes, combining AGPLv3-only code into an AGPL-3.0-or-later work is permitted; the combined work is distributed under plain AGPLv3 terms `[V]` | Cloned the repository at `/tmp/syncthing-socket` on 2026-09-18 and read `LICENSE` in full: canonical GNU AGPL v3 text, no "or later" grant in the file itself. |
| filippo.io/nistec v0.0.4 | BSD-3-Clause | Yes, permissive, compatible `[V]` | `go get`'d into `go.mod`; read `LICENSE` at `$(go env GOMODCACHE)/filippo.io/nistec@v0.0.4/LICENSE` in full: standard 3-clause BSD (Go Authors / Google LLC copyright). |
| golang.org/x/sys | BSD-3-Clause | Yes `[V]` | Transitive dependency of nistec; same licence family as the Go standard toolchain, read `LICENSE` in the module cache. |
| com.android.tools.build:gradle 8.3.2 (Android Gradle Plugin, `android/wp0-spike`) | Apache-2.0 `[V]` | Yes, Apache-2.0 is a permissive licence compatible with AGPL-3.0-or-later `[V]` | Build-time only, not linked into the app. Read the `<licenses>` block in the resolved POM at `~/.gradle/caches/modules-2/files-2.1/com.android.tools.build/gradle/8.3.2/.../gradle-8.3.2.pom` on 2026-09-18: "The Apache Software License, Version 2.0". |
| org.jetbrains.kotlin:kotlin-gradle-plugin 1.9.22 (`android/wp0-spike`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Build-time only. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache License, Version 2.0". |
| androidx.test.ext:junit 1.1.5 (`android/wp0-spike/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Test-only, packaged into the androidTest APK, not distributed to end users. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache Software License, Version 2.0". |
| androidx.test:runner 1.5.2 (`android/wp0-spike/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Test-only, same as above. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache Software License, Version 2.0". |
| junit:junit 4.13.2 (transitive, via androidx.test.ext:junit/androidx.test:runner, `android/wp0-spike/app`) | Eclipse Public License 1.0 `[V]` | Yes, for this use, with a caveat `[U]`: the FSF lists EPL-1.0 as GPL-incompatible for a combined/linked work, but here JUnit4 is a test-only dependency packaged solely into the androidTest APK, which this project builds for local/CI test runs and does not distribute to end users; no distributed artefact links AGPL-3.0-or-later code with JUnit4 code. Flag for re-check if a test artefact is ever distributed rather than run-and-discard. | Read the resolved POM's `<licenses>` block at `~/.gradle/caches/.../junit/junit/4.13.2/.../junit-4.13.2.pom` on 2026-09-18: "Eclipse Public License 1.0". |

## Copied and adapted code

Not a dependency (nothing imported), but licence-relevant: this repository
contains code copied from another AGPL-3.0 project and adapted, per the task
brief's explicit permission to do so.

| What | From | Adaptation |
|---|---|---|
| `dracut/90unlocker/{module-setup,unlocker-start,unlocker-stop}.sh` | `syncthing-socket`'s `contrib/dracut-luks/90syncthing-socket/` (commit checked out at `/tmp/syncthing-socket` on 2026-09-18) | Renamed the binary, PID file, kernel command-line flag (`rd.syncthing_socket=0` to `rd.unlocker=0`), and hook filenames; dropped the `/etc/syncthing-socket/luks.conf` override (this project's token is the only configuration source); comments and licence header adjusted to this project's SPDX convention. Same AGPL-3.0 licence, both AGPL-3.0-or-later compatible per the row above. |

## Pending

- Full transitive dependency audit of `syncthing-socket`'s `go.mod` (pion/webrtc,
  syncthing/syncthing, hashicorp/yamux, and others). These are pulled in only
  through the library import, not vendored or copied, so their licences govern
  linking, not copying; each will be checked and added here as WP2 (transport)
  lands and actually imports the package. `[U]`
- Android/Kotlin and Gradle plugin dependencies, added as WP5 starts.
