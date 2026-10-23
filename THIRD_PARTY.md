# Third-party licences

Living document. Every dependency added to `go.mod`, `android/`, or vendored
from another repository is recorded here with how its licence was checked,
before the commit that adds it lands.

| Dependency | Licence | Compatible with AGPL-3.0-or-later | How checked |
|---|---|---|---|
| github.com/muelli/syncthing-socket v0.1.1-alpha.0.20261018051137-204ecb48b52c (release v164) | AGPL-3.0-or-later `[V]` | Yes, same licence `[V]` | Imports only two of its packages, both standard-library-only: `github.com/muelli/syncthing-socket/identity` (`GenerateDeterministicCert`, in `mobile/`) and `github.com/muelli/syncthing-socket/discovery` (`LookupRecord`, `DefaultURL`, `DefaultAnnounceInterval`, in `mrcore/transport/syncthing_relay.go`). The root package (CLI, WebRTC, SOCKS) is not imported, so none of its dependencies are linked. Read `LICENSE` in the module cache on 2026-10-22: canonical GNU AGPL v3 text. All 50 `.go` files in that version carry an SPDX header naming AGPL-3.0-or-later, checked by `grep -rl SPDX-License-Identifier` against `find -name '*.go'`. The SBOM tool (`syft`) reports `AGPL-3.0-only` for it, because it classifies the `LICENSE` text, which has no "or later" wording, and does not read the per-file headers; both are within AGPL-3.0-or-later terms for this work. |
| github.com/stretchr/testify v1.11.1 | MIT `[V]` | Yes, permissive, compatible `[V]` | Listed in `go.mod` as indirect only because `go mod tidy` needs it for the tests of `github.com/syncthing/syncthing/lib/protocol`, which `mrcore/transport` imports; it was previously satisfied by a module that is no longer a dependency. Not linked into any binary: `go list -deps -test ./...` shows no `stretchr` package `[V]`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/stretchr/testify@v1.11.1/LICENSE` on 2026-10-22: MIT. |
| filippo.io/nistec v0.0.4 | BSD-3-Clause | Yes, permissive, compatible `[V]` | `go get`'d into `go.mod`; read `LICENSE` at `$(go env GOMODCACHE)/filippo.io/nistec@v0.0.4/LICENSE` in full: standard 3-clause BSD (Go Authors / Google LLC copyright). |
| golang.org/x/sys | BSD-3-Clause | Yes `[V]` | Transitive dependency of nistec; same licence family as the Go standard toolchain, read `LICENSE` in the module cache. |
| com.android.tools.build:gradle 8.3.2 (Android Gradle Plugin, `android/wp0-spike`) | Apache-2.0 `[V]` | Yes, Apache-2.0 is a permissive licence compatible with AGPL-3.0-or-later `[V]` | Build-time only, not linked into the app. Read the `<licenses>` block in the resolved POM at `~/.gradle/caches/modules-2/files-2.1/com.android.tools.build/gradle/8.3.2/.../gradle-8.3.2.pom` on 2026-09-18: "The Apache Software License, Version 2.0". |
| org.jetbrains.kotlin:kotlin-gradle-plugin 1.9.22 (`android/wp0-spike`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Build-time only. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache License, Version 2.0". |
| org.cyclonedx:cyclonedx-gradle-plugin 2.3.1 (`android`, SBOM generation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Build-time only, not linked into the app: it adds a `cyclonedxBom` task and nothing else, and the APK is byte-for-byte unaffected by its presence. Version 2.x rather than the current 3.x deliberately: 3.x dropped `includeConfigs`, without which the SBOM lists Espresso and the Compose test libraries as though they shipped in the APK. Read `LICENSE` at https://github.com/CycloneDX/cyclonedx-gradle-plugin on 2026-09-21: canonical Apache License 2.0 text (the published POM carries no `<licenses>` block). |
| Gradle wrapper 8.7 (`android/gradlew`, `android/gradlew.bat`, `android/gradle/wrapper/`, and the same files under `android/wp0-spike/`) | Apache-2.0 `[V]` | Yes, Apache-2.0 is a permissive licence compatible with AGPL-3.0-or-later `[V]` | Vendored in the repository, so `REUSE.toml` declares it Apache-2.0 instead of AGPL. Not linked into the app: it only downloads and starts Gradle at build time. Read on 2026-10-26: the header of `android/gradlew` (Apache License 2.0, "Copyright 2015-2021 the original authors") and `META-INF/LICENSE` inside `gradle-wrapper.jar` (the Apache License 2.0 text); the two `gradle-wrapper.jar` files are byte-identical (`cmp`). |
| androidx.test.ext:junit 1.1.5 (`android/wp0-spike/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Test-only, packaged into the androidTest APK, not distributed to end users. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache Software License, Version 2.0". |
| androidx.test:runner 1.5.2 (`android/wp0-spike/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Test-only, same as above. Read the resolved POM's `<licenses>` block on 2026-09-18: "The Apache Software License, Version 2.0". |
| junit:junit 4.13.2 (transitive, via androidx.test.ext:junit/androidx.test:runner, `android/wp0-spike/app`) | Eclipse Public License 1.0 `[V]` | Yes, for this use, with a caveat `[U]`: the FSF lists EPL-1.0 as GPL-incompatible for a combined/linked work, but here JUnit4 is a test-only dependency packaged solely into the androidTest APK, which this project builds for local/CI test runs and does not distribute to end users; no distributed artefact links AGPL-3.0-or-later code with JUnit4 code. Flag for re-check if a test artefact is ever distributed rather than run-and-discard. | Read the resolved POM's `<licenses>` block at `~/.gradle/caches/.../junit/junit/4.13.2/.../junit-4.13.2.pom` on 2026-09-18: "Eclipse Public License 1.0". |
| github.com/syncthing/syncthing v1.30.0 | MPL-2.0 | Yes, MPL-2.0 permits combining with a work under other licence terms; the combined work is distributed under AGPL-3.0-or-later, MPL-2.0-covered files stay under MPL-2.0 `[V]` | Imported directly for `lib/relay/client` and `lib/protocol` (mrcore/transport/syncthing_relay.go). Read `LICENSE` in the module cache on 2026-09-18 in full: canonical Mozilla Public License Version 2.0 text. |
| github.com/beorn7/perks v1.0.1 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/beorn7/perks` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/beorn7/perks@v1.0.1/LICENSE` on 2026-09-18 in full. |
| github.com/calmh/xdr v1.2.0 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/calmh/xdr` on 2026-10-22 gives `github.com/muelli/synctang/mrcore/transport > github.com/syncthing/syncthing/lib/relay/client > github.com/syncthing/syncthing/lib/relay/protocol`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/calmh/xdr@v1.2.0/LICENSE` on 2026-09-18 in full. |
| github.com/cespare/xxhash/v2 v2.3.0 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/cespare/xxhash/v2` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE.txt` at `$(go env GOMODCACHE)/github.com/cespare/xxhash/v2@v2.3.0/LICENSE.txt` on 2026-09-18 in full. |
| github.com/coreos/go-systemd/v22 v22.7.0 | Apache-2.0 | Yes, permissive, compatible `[V]` | Called directly (`journal.Enabled`/`journal.Send` in `cmd/unlocker/log.go`, for structured trace-through-error logging to the systemd journal). Read `LICENSE` at `$(go env GOMODCACHE)/github.com/coreos/go-systemd/v22@v22.7.0/LICENSE` on 2026-09-18 in full. |
| github.com/hashicorp/golang-lru/v2 v2.0.7 | MPL-2.0 | Yes, MPL-2.0 permits combining with a work under other licence terms; the combined work is distributed under AGPL-3.0-or-later, MPL-2.0-covered files stay under MPL-2.0 `[V]` | Transitive dependency: `go mod why -m github.com/hashicorp/golang-lru/v2` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/hashicorp/golang-lru/v2`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/hashicorp/golang-lru/v2@v2.0.7/LICENSE` on 2026-09-18 in full. |
| github.com/mdp/qrterminal/v3 v3.2.1 | MIT | Yes, permissive, compatible `[V]` | Now a direct dependency: `cmd/unlocker/qrcode.go` uses it to draw the pairing QR code on the machine's own console, so `unlocker pair` needs no `qrencode` binary installed. Until 2026-10-22 it was also linked in transitively through `syncthing-socket`'s root package; that package is no longer imported, so this direct import is now the only reason it is in the build. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/mdp/qrterminal/v3@v3.2.1/LICENSE` in full on 2026-09-22: MIT, Mark Percival. |
| github.com/miscreant/miscreant.go v0.0.0-20200214223636-26d376326b75 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/miscreant/miscreant.go` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/miscreant/miscreant.go`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE.txt` at `$(go env GOMODCACHE)/github.com/miscreant/miscreant.go@v0.0.0-20200214223636-26d376326b75/LICENSE.txt` on 2026-09-18 in full. |
| github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/munnerz/goautoneg` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/munnerz/goautoneg@v0.0.0-20191010083416-a7dc8b61c822/LICENSE` on 2026-09-18 in full. |
| github.com/pierrec/lz4/v4 v4.1.22 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/pierrec/lz4/v4` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/pierrec/lz4/v4`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/pierrec/lz4/v4@v4.1.22/LICENSE` on 2026-09-18 in full. |
| github.com/prometheus/client_golang v1.22.0 | Apache-2.0 | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/prometheus/client_golang` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/prometheus/client_golang@v1.22.0/LICENSE` on 2026-09-18 in full. |
| github.com/prometheus/client_model v0.6.1 | Apache-2.0 | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/prometheus/client_model` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/prometheus/client_model@v0.6.1/LICENSE` on 2026-09-18 in full. |
| github.com/prometheus/common v0.62.0 | Apache-2.0 | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/prometheus/common` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/prometheus/common@v0.62.0/LICENSE` on 2026-09-18 in full. |
| github.com/prometheus/procfs v0.15.1 | Apache-2.0 | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/prometheus/procfs` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > github.com/prometheus/client_golang/prometheus`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/prometheus/procfs@v0.15.1/LICENSE` on 2026-09-18 in full. |
| github.com/shirou/gopsutil/v4 v4.25.4 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/shirou/gopsutil/v4` on 2026-10-22 gives `github.com/muelli/synctang/mrcore/transport > github.com/syncthing/syncthing/lib/relay/client > github.com/syncthing/syncthing/lib/osutil`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/shirou/gopsutil/v4@v4.25.4/LICENSE` on 2026-09-18 in full. |
| github.com/syncthing/notify v0.0.0-20250528144937-c7027d4f7465 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/syncthing/notify` on 2026-10-22 gives `github.com/muelli/synctang/mrcore/transport > github.com/syncthing/syncthing/lib/relay/client > github.com/syncthing/syncthing/lib/osutil`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/syncthing/notify@v0.0.0-20250528144937-c7027d4f7465/LICENSE` on 2026-09-18 in full. |
| github.com/thejerf/suture/v4 v4.0.6 | MIT | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m github.com/thejerf/suture/v4` on 2026-10-22 gives `github.com/muelli/synctang/mrcore/transport > github.com/syncthing/syncthing/lib/relay/client > github.com/thejerf/suture/v4`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/github.com/thejerf/suture/v4@v4.0.6/LICENSE` on 2026-09-18 in full. |
| golang.org/x/crypto v0.54.0 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m golang.org/x/crypto` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > golang.org/x/crypto/chacha20poly1305`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/golang.org/x/crypto@v0.54.0/LICENSE` on 2026-09-18 in full. |
| golang.org/x/net v0.57.0 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m golang.org/x/net` on 2026-10-22 gives `github.com/muelli/synctang/mrcore/transport > github.com/syncthing/syncthing/lib/relay/client > github.com/syncthing/syncthing/lib/dialer`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/golang.org/x/net@v0.57.0/LICENSE` on 2026-09-18 in full. |
| golang.org/x/term v0.45.0 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m golang.org/x/term` on 2026-10-22 gives `github.com/muelli/synctang/cmd/unlocker > github.com/mdp/qrterminal/v3 > golang.org/x/term`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/golang.org/x/term@v0.45.0/LICENSE` on 2026-09-18 in full. |
| golang.org/x/text v0.40.0 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m golang.org/x/text` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > golang.org/x/text/unicode/norm`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/golang.org/x/text@v0.40.0/LICENSE` on 2026-09-18 in full. |
| google.golang.org/protobuf v1.36.6 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m google.golang.org/protobuf` on 2026-10-22 gives `github.com/muelli/synctang/cmd/keyholder > github.com/syncthing/syncthing/lib/protocol > google.golang.org/protobuf/proto`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/google.golang.org/protobuf@v1.36.6/LICENSE` on 2026-09-18 in full. |
| rsc.io/qr v0.2.0 | BSD-3-Clause | Yes, permissive, compatible `[V]` | Transitive dependency: `go mod why -m rsc.io/qr` on 2026-10-22 gives `github.com/muelli/synctang/cmd/unlocker > github.com/mdp/qrterminal/v3 > rsc.io/qr`, so it is linked via Syncthing's libraries or `qrterminal`, not via `syncthing-socket`. Read `LICENSE` at `$(go env GOMODCACHE)/rsc.io/qr@v0.2.0/LICENSE` on 2026-09-18 in full. |

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
- Update on the bullet above: WP2 (transport) landed and now imports `syncthing-socket`
  and `github.com/syncthing/syncthing`. Checked, on 2026-09-18, every module that
  `go list -deps -test ./...` actually reports as compiled into this repository's own
  build on Linux (the platform this project targets); each got its own row above.
  Five further `go.mod` entries (`github.com/ebitengine/purego`, `github.com/go-ole/go-ole`,
  `github.com/inconshreveable/mousetrap`, `github.com/power-devops/perfstat`,
  `github.com/yusufpapurcu/wmi`) are present only for Windows/Darwin build
  constraints inside `gopsutil` and `cobra`'s dependency graph and are never
  compiled for this project's Linux target; left unchecked here, flag for
  a licence check only if a non-Linux build is ever produced. `[U]`

## Android key holder app dependencies (WP5)

Every row below was checked on 2026-09-18 by reading the `<licenses>` block of
the resolved POM in the Gradle cache
(`~/.gradle/caches/modules-2/files-2.1/<group>/<artifact>/<version>/*/`), with
`scripts/check-pom-licences.sh`, rather than by consulting a project web page.
Versions are the ones Gradle actually resolved for `android/app`, which for the
Compose artefacts is what `androidx.compose:compose-bom:2024.02.02` pins.

All of these are Apache-2.0, permissive, and compatible with
AGPL-3.0-or-later. None of them is Google Play Services, Firebase or ML Kit:
F-Droid's scanner rejects those, and the QR scanning that would usually pull
one in is done by CameraX plus ZXing's pure-Java core instead.

| Dependency | Licence | Compatible with AGPL-3.0-or-later | How checked |
|---|---|---|---|
| androidx.core:core-ktx 1.12.0 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.fragment:fragment-ktx 1.6.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". Needed because `BiometricPrompt` takes a `FragmentActivity`. |
| androidx.activity:activity-compose 1.8.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.lifecycle:lifecycle-runtime-ktx 2.7.0 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.lifecycle:lifecycle-viewmodel-compose 2.7.0 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.compose:compose-bom 2024.02.02 (`android/app`, platform) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". A BOM pins versions and ships no code itself. |
| androidx.compose.ui:ui 1.6.3 (via the BOM) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.compose.ui:ui-graphics 1.6.3 (via the BOM) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.compose.ui:ui-tooling-preview 1.6.3 (via the BOM) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.compose.ui:ui-tooling 1.6.3 (via the BOM, debug builds only) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". `debugImplementation`, so not in a release APK. |
| androidx.compose.ui:ui-test-manifest 1.6.3 (via the BOM, debug builds only) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". `debugImplementation`, so not in a release APK. |
| androidx.compose.ui:ui-test-junit4 1.6.3 (via the BOM, `android/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". Test-only, packaged into the androidTest APK, not distributed to end users. |
| androidx.compose.material3:material3 1.2.1 (via the BOM) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.compose.material:material-icons-core 1.6.3 (transitive, via material3) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Already on the classpath as a dependency of material3 (`./gradlew :app:dependencies`, 2026-10-23); now used directly for the Info and back arrow icons in the app bar. Licence read from the published POMs of `material-icons-core` and `material-icons-core-android` 1.6.3 on Google's Maven repository: "The Apache Software License, Version 2.0". Not the 11 MB material-icons-extended. |
| androidx.biometric:biometric 1.1.0 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.security:security-crypto 1.1.0-alpha06 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". Used for the pairing record only; no MR-1 secret is ever stored through it. |
| androidx.camera:camera-core 1.3.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.camera:camera-camera2 1.3.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.camera:camera-lifecycle 1.3.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| androidx.camera:camera-view 1.3.2 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". |
| com.google.zxing:core 3.5.3 (`android/app`) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | The artefact POM inherits from `com.google.zxing:zxing-parent:3.5.3`, which is what carries the `<licenses>` block; read it in the Gradle cache on 2026-09-18: "The Apache Software License, Version 2.0". Pure Java, no Play Services, no ML Kit. |
| androidx.test:rules 1.5.0 (`android/app`, androidTestImplementation) | Apache-2.0 `[V]` | Yes, permissive, compatible `[V]` | Resolved POM's `<licenses>`: "The Apache Software License, Version 2.0". Test-only, packaged into the androidTest APK, not distributed. |
| golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e | BSD-3-Clause `[V]` | Yes, permissive, compatible `[V]` | Added to `go.mod` by `go get -tool golang.org/x/mobile/cmd/gobind`, which current `gomobile bind` requires. Both a build tool (gobind) and a real runtime dependency: `gomobile bind` links `golang.org/x/mobile/bind` into `libgojni.so` and generates `go/Seq.java`, `go/Universe.java` and `go/error.java` from it into the `.aar`, so this code ships in the app. Read `LICENSE` at `$(go env GOMODCACHE)/golang.org/x/mobile@v0.0.0-20260908204917-8b95e45f8d3e/LICENSE` on 2026-09-18 in full: standard 3-clause BSD, "Copyright 2009 The Go Authors". |

The same `go get -tool` also moved several `golang.org/x/*` modules that already
have rows above to newer versions (`crypto` v0.54.0 to v0.57.0, `image` v0.44.0
to v0.46.0, `net` v0.57.0 to v0.59.0, `sys` v0.47.0 to v0.48.0, `term` v0.45.0
to v0.46.0, `text` v0.40.0 to v0.42.0, plus `mod`, `sync` and `tools` as tool
dependencies). All are BSD-3-Clause, unchanged from the rows above; the module
cache `LICENSE` files for the new versions were spot-checked on 2026-09-18 and
are byte-identical to the ones already recorded. `[V]`

Also now used by `android/app`, not only by `android/wp0-spike` as their rows
above say: `com.android.tools.build:gradle` 8.3.2 and
`org.jetbrains.kotlin:kotlin-gradle-plugin` 1.9.22 (both build-time only, both
Apache-2.0 as already checked), and `androidx.test.ext:junit` 1.1.5,
`androidx.test:runner` 1.5.2 and `junit:junit` 4.13.2 (all test-only, packaged
into the androidTest APK and not distributed, with the EPL-1.0 caveat recorded
in the JUnit4 row above applying unchanged here).

## Publishing toolchain (CI only, not shipped)

These run in the publish workflow and in `scripts/`; none of them is linked
into any distributed artefact.

| Dependency | Licence | Compatible? | Evidence |
|---|---|---|---|
| fdroidserver (`pip install fdroidserver`, CI only) | AGPL-3.0-or-later `[V]` | Yes, same licence as this project `[V]` | Generates and signs the repository index. Read the project's own `setup.py` classifier and LICENSE on 2026-09-21. |
| python-cryptography (`scripts/derive-signing-key.py`, CI and local) | Apache-2.0 OR BSD-3-Clause `[V]` | Yes, permissive `[V]` | Derives the signing identities. Dual-licensed; either arm is compatible. |
| qrencode (CI only) | LGPL-2.1-or-later `[V]` | Yes, invoked as a separate program `[V]` | Renders the add-repository QR code on the landing page. |
| syft (`scripts/generate-sbom.sh`, CI and local) | Apache-2.0 `[V]` | Yes, permissive, and invoked as a separate program rather than linked `[V]` | Reads the Go module list out of built binaries to produce their SBOMs. Read `LICENSE` at https://github.com/anchore/syft on 2026-09-21 in full: canonical Apache License 2.0 text. |
| reuse (`pip install reuse`, CI and local) | Apache-2.0 AND CC0-1.0 AND CC-BY-SA-4.0 AND GPL-3.0-or-later `[V]` | Yes, invoked as a separate program and nothing from it is linked or shipped `[V]` | Checks that every file declares its licence and copyright (`reuse lint`). The licence expression is the package's own metadata (`pip show reuse`, version 6.2.0) and the licence texts shipped in its `dist-info/licenses` directory. |
| librsvg2-bin / rsvg-convert (`scripts/render-icon.sh`, local only) | LGPL-2.1-or-later `[V]` | Yes, invoked as a separate program `[V]` | Renders `android/artwork/icon.svg` to the PNG F-Droid displays. |

- Update 2026-10-22: `syncthing-socket` is now imported only through its `identity` and
  `discovery` packages (release v164), so the modules that only its root package
  needed (`pion/*`, `spf13/cobra`, `spf13/pflag`, `go-socks5`, `go-termimg`, `yamux`,
  `kardianos/service`, `pquerna/otp`, `go-proxyproto`, and the rest of that list)
  left `go.mod`, and their rows are gone with them. `github.com/inconshreveable/mousetrap`
  left with `cobra`. The four Windows/Darwin-only entries named above that came from
  `gopsutil` remain and remain unchecked `[U]`. `github.com/stretchr/testify` is new in
  `go.mod` as an indirect, test-only entry, see its row.
