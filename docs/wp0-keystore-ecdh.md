# WP0: Android Keystore ECDH against a manually-constructed point

Status: spike complete on emulator, no real device tested. `[U]` for
everything under "For the human", `[V]` for everything else, verified as
described inline.

## Question tested

Does Android Keystore ECDH (`KeyAgreement.getInstance("ECDH")` against a
private key held in the `AndroidKeyStore`) accept a manually-constructed
P-256 public key as the peer key, given as raw `(x, y)` coordinates rather
than a `PublicKey` produced by a `KeyPairGenerator`, and does it return the
correct raw x-coordinate of `s·X` (not a KDF'd value)?

This gates whether the machine-side of the MR-1 protocol, which builds the
peer point `X` purely by curve arithmetic (`X = C + E`, never through a
`KeyPairGenerator`), can do ECDH against a phone-held Keystore/StrongBox key
at all. If the Keystore rejects such a point, or hands back something other
than the raw x-coordinate, the phone-side key cannot live in the Keystore in
the way the design assumes.

## Test design

As specified in the work package, no deviation:

1. Generate a software EC keypair `e`/`E` with `KeyPairGenerator.getInstance("EC")`
   (default provider, NOT `AndroidKeyStore`), NIST P-256 / secp256r1.
   Exportable, `e` known to the test.
2. Read `E`'s raw `(x, y)` as `BigInteger`s off the `ECPublicKey`. Rebuild
   `E'` via `KeyFactory.getInstance("EC").generatePublic(ECPublicKeySpec(ECPoint(x, y), params))`.
   This is the object under test: a `PublicKey` built from raw coordinates,
   never touched by a `KeyPairGenerator`, standing in for the arbitrary
   curve point the machine side builds by point addition.
3. Generate a Keystore EC keypair `s`/`S` via `KeyGenParameterSpec`,
   `KeyProperties.KEY_ALGORITHM_EC`, P-256, purpose `PURPOSE_AGREE_KEY`,
   `setUserAuthenticationRequired(false)` (biometric gating is the separate
   manual real-device step below). `setIsStrongBoxBacked(true)` first; on
   `StrongBoxUnavailableException`, fall back to
   `setIsStrongBoxBacked(false)` and log which path ran. `s` never leaves
   the Keystore; `S` is read back via `KeyStore.getCertificate(alias).getPublicKey()`.
4. `A = KeyAgreement.getInstance("ECDH")`, `init(s)`, `doPhase(E', true)`,
   `generateSecret()`: the shared secret computed inside the
   Keystore/StrongBox against the manually-constructed peer point.
5. `B = KeyAgreement.getInstance("ECDH")`, `init(e)`, `doPhase(S, true)`,
   `generateSecret()`: the same computation done entirely in software,
   against the Keystore's own public key.
6. Assert `A == B` (both equal `x(s·e·G) = x(e·s·G)` by commutativity: this
   proves correctness without the test ever needing to know `s`). Assert
   the byte length is 32 (P-256 field size, raw x-coordinate, not
   hashed/KDF'd).
7. The independent "is this really the x-coordinate" check from the work
   package is exactly step 5: `B` is a second, fully independent
   software-only ECDH against `S`. No further computation was needed; the
   assertion message in the test spells out what the equality proves.

Test code: `android/wp0-spike/app/src/androidTest/java/org/synctang/wp0spike/KeystoreEcdhSpikeTest.kt`.

## Environment

- Host: Ubuntu 26.04 LTS container, 4 vCPU, 11 GiB RAM, `/dev/kvm` present.
  `[V]` `cat /etc/os-release`, `nproc`, `free -h`, `ls /dev/kvm`.
- KVM was present but the running user lacked group membership; fixed with
  `sudo gpasswd -a ubuntu kvm` and running the emulator via `sg kvm -c '...'`
  rather than a fresh login shell (group membership does not apply to an
  already-running shell without this). `[V]` emulator ran hardware-accelerated
  after this fix (see emulator log below).
- Java: Eclipse Temurin is not in this distribution's default apt sources;
  used the distribution's own OpenJDK 21 instead
  (`openjdk-21-jdk-headless`, build `21.0.12+8-1-26.04-Ubuntu`), installed
  via `apt-get install openjdk-21-jdk-headless`. `[V]` `java -version`.
- Gradle: 8.7 (binary distribution), downloaded directly from
  `services.gradle.org` since the distro's `apt` Gradle (4.4.1) is too old
  for a current Android Gradle Plugin. Used once to generate the project's
  own wrapper (`gradle wrapper --gradle-version 8.7 --distribution-type bin`);
  all subsequent builds run through `./gradlew`, which is what is committed.
  `[V]` `gradle -v`.
- Android Gradle Plugin: 8.3.2. Kotlin: 1.9.22 (matches the Kotlin DSL
  bundled with Gradle 8.7, avoiding a version mismatch). `[V]` set directly
  in `android/wp0-spike/build.gradle.kts`, resolved without error.
- Android SDK: cmdline-tools `11076708`, installed to `/home/ubuntu/android-sdk`.
  Packages: `platform-tools`, `platforms;android-34`, `build-tools;34.0.0`,
  `emulator`, `system-images;android-34;google_apis;x86_64`. `[V]`
  `sdkmanager --list_installed` succeeded, exit 0, all packages present.
- AVD: `wp0spike`, device profile `pixel`, system image
  `system-images;android-34;google_apis;x86_64` (API 34, Android 14,
  x86_64). `[V]` `adb shell getprop ro.build.version.release` → `14`,
  `ro.build.version.sdk` → `34`, `ro.product.cpu.abi` → `x86_64`.
- Emulator launch flags: `-no-window -no-audio -no-boot-anim -no-snapshot
  -gpu swiftshader_indirect -accel on`, wrapped in `timeout 1800`. Booted
  cold in under 80 s both times it was started (`77997 ms` first boot after
  the KVM permission fix failed and it was restarted, `54660 ms` on the run
  actually used for the test below). `[V]` `emulator` log, `INFO | Boot
  completed in ... ms` lines; `adb shell getprop sys.boot_completed` → `1`.

## StrongBox outcome

Expected and confirmed: the emulator has no StrongBox hardware, so
`setIsStrongBoxBacked(true)` threw `StrongBoxUnavailableException` and the
test fell back to a non-StrongBox Keystore key. This is expected and is
exactly why the work package frames "try StrongBox first" as the
automatable path and defers the real StrongBox check to a human on real
hardware (see below).

- `StrongBoxUnavailableException` thrown: **yes** `[V]`
- Fallback path used (non-StrongBox `AndroidKeyStore` key): **yes** `[V]`

Verified by running `./gradlew connectedAndroidTest` against the
`wp0spike` AVD (API 34) on 2026-09-18 and reading the test's own logcat
output (`adb logcat -d -s WP0Spike:I`):

```
09-18 17:09:07.159  4773  4792 I WP0Spike: StrongBoxUnavailableException thrown (expected on the emulator): Failed to generated key pair.
09-18 17:09:07.174  4773  4792 I WP0Spike: strongBoxUnavailableThrown=true strongBoxBacked=false
```

## Result: core assertion

- Core assertion (`A == B`): **PASS** `[V]`
- `secretA` (hex, 64 hex digits = 32 bytes): `c47778197de949192576abf57739d992d176bcd33b2377b16c822224baf102e2`
- `secretB` (hex): identical to `secretA` (that is precisely what the core
  assertion checks).
- `generateSecret()` byte length: **32 bytes**, i.e. the raw P-256 x-coordinate,
  not hashed/KDF'd. `[V]`

Verified by the same run, reading the test's logcat output:

```
09-18 17:09:07.269  4773  4792 I WP0Spike: secretA(hex)=c47778197de949192576abf57739d992d176bcd33b2377b16c822224baf102e2 len=32
09-18 17:09:07.270  4773  4792 I WP0Spike: secretB(hex)=c47778197de949192576abf57739d992d176bcd33b2377b16c822224baf102e2 len=32
```

and the JUnit XML report
(`android/wp0-spike/app/build/outputs/androidTest-results/connected/debug/TEST-wp0spike(AVD) - 14-_app-.xml`):

```xml
<testsuite name="org.synctang.wp0spike.KeystoreEcdhSpikeTest" tests="1" failures="0" errors="0" skipped="0" time="1.146" timestamp="2026-09-18T17:09:09" hostname="localhost">
  <testcase name="keystoreEcdhAcceptsManuallyConstructedPeerPointAndReturnsRawXCoordinate" classname="org.synctang.wp0spike.KeystoreEcdhSpikeTest" time="0.02" />
</testsuite>
```

`tests="1" failures="0" errors="0"`: the single test method, covering both
JUnit assertions (`secretA.size == 32`, and `secretA` equals `secretB`
byte-for-byte), passed.

### What this proves

The Android Keystore's ECDH implementation (non-StrongBox path, on this API
34 emulator) accepted a `PublicKey` built from raw `(x, y)` coordinates via
`ECPublicKeySpec`, one it never produced itself and never saw as an
opaque handle, as the peer key to `doPhase()`. `generateSecret()` returned
exactly 32 bytes, and those 32 bytes matched an entirely independent
software-only ECDH computation against the Keystore's own public key. That
independent match is only possible if both sides computed the same
x-coordinate of the same curve point: had the Keystore instead applied an
internal KDF, ignored the point and used a fixed or attacker-uncontrolled
value, or silently coerced the point onto a different curve, `secretA` and
`secretB` would not agree, since `secretB` never goes near the
Keystore's internals; it is computed by ordinary `java.security` code
against the public key material read out of the certificate. The
API-plumbing risk named in the work package (does Android's
`KeyAgreement` accept a `PublicKey` built from raw `ECPublicKeySpec`
coordinates) is resolved: yes, on this emulator's non-StrongBox backend.

## Fallback (not implemented; only relevant if the core assertion above is FAIL)

If the Keystore rejected the manually-constructed point, or returned
something other than the raw x-coordinate, the design's fallback is: keep
the phone-side long-term secret in app-private storage, encrypted with a
Keystore-held AES key (itself biometric-gated), rather than as a Keystore
EC private key doing ECDH directly. The AES key never leaves the Keystore;
the encrypted scalar is exportable in principle (it is just ciphertext in
app storage), and biometric gating happens on the AES `Cipher.doFinal` /
`unwrap` call instead of on `KeyAgreement.doPhase`. This is a downgrade: the
private scalar exists in plaintext in process memory during use, whereas
the direct-Keystore-ECDH path never materialises it outside the secure
element/TEE. Not implemented here; needs a decision from the human first,
per the work package's stop condition.

## For the human: real-device StrongBox + BiometricPrompt retest

This emulator run only proves the API-plumbing question (does Keystore
ECDH accept a manually-constructed point, and return the raw x-coordinate)
on a software-only, non-StrongBox, non-biometric-gated Keystore backend.
It does **not** prove StrongBox itself accepts such a point, and does not
prove a biometric-gated `doPhase()` call behaves the same way. Both need a
real device.

### What you need

- An Android phone with StrongBox hardware (most devices from roughly 2018
  onward with a dedicated secure element; check
  `PackageManager.FEATURE_STRONGBOX_KEYSTORE` if unsure) and a fingerprint
  or face unlock already enrolled.
- USB cable, developer options + USB debugging enabled on the phone.
- `adb` on this machine or any machine with the phone plugged in
  (`/home/ubuntu/android-sdk/platform-tools/adb`, or any `adb` from a
  normal Android SDK install).

### Steps

1. Plug in the phone, accept the "allow USB debugging" prompt on the phone
   screen when it appears.
2. From this repository, build and install the spike's test APKs onto the
   phone (not the emulator) with:
   ```
   cd android/wp0-spike
   ANDROID_SERIAL=<serial-from-adb-devices> ./gradlew connectedAndroidTest
   ```
   Run `adb devices` first to get `<serial-from-adb-devices>` (it will be a
   device ID, not `emulator-5554`).
3. The current test (`KeystoreEcdhSpikeTest.keystoreEcdhAcceptsManuallyConstructedPeerPointAndReturnsRawXCoordinate`)
   calls `setUserAuthenticationRequired(false)`, so it will run without a
   biometric prompt even on the real device: it will just tell you whether
   `setIsStrongBoxBacked(true)` succeeds instead of throwing
   `StrongBoxUnavailableException` this time. Watch the terminal output (or
   `adb logcat -s WP0Spike:I` in a second terminal while the test runs) for:
   ```
   strongBoxUnavailableThrown=... strongBoxBacked=...
   secretA(hex)=... len=...
   secretB(hex)=... len=...
   ```
   Copy that block back. If `strongBoxUnavailableThrown=false` and
   `strongBoxBacked=true`, StrongBox itself accepted the manually
   constructed point and the core assertion passing means StrongBox is
   confirmed, not just the software Keystore backend.
4. For the biometric-gated variant, a second, separate test is needed (not
   in this spike, since it cannot run unattended): change
   `setUserAuthenticationRequired(false)` to
   `setUserAuthenticationRequired(true)` (with
   `setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)`
   on API 30+) in a copy of `KeystoreEcdhSpikeTest.kt`, and wrap the
   `agreementA.doPhase(...)` call in a `BiometricPrompt` flow instead of
   calling it directly from a test thread (a JUnit instrumented test cannot
   itself tap "confirm fingerprint" on the screen). Practically, this means
   turning the spike into a one-screen manual app instead of an automated
   test: a button that runs steps 1-3 automatically, then a button that
   calls `agreementA.doPhase()` behind a `BiometricPrompt.authenticate()`
   call, showing the resulting hex on screen for you to read and type back.
   This is future work, not built as part of this spike; flagging it here
   since the work package asked for exact instructions, and building the
   actual manual-test app is outside what "instructions for a human" can
   cover on its own. **`[I]`** marking this because it describes a plan for
   a not-yet-built manual test app, not a verified procedure.
5. Report back: the `strongBoxUnavailableThrown` / `strongBoxBacked` values
   from step 3, and whether the core assertion passed on the real device
   (the test fails loudly with an `AssertionError` and a non-zero
   `./gradlew` exit code if it does not; no separate "did it pass" step is
   needed beyond checking the command's exit code and the printed
   `secretA`/`secretB` hex, which should be identical if it passed).

## Files

- `android/wp0-spike/`: self-contained Gradle/Kotlin project, not wired into
  any future `android/app/`.
- `android/wp0-spike/app/src/androidTest/java/org/synctang/wp0spike/KeystoreEcdhSpikeTest.kt`:
  the test itself.
- `THIRD_PARTY.md`: dependency rows for everything this module pulls in.
