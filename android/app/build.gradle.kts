// SPDX-License-Identifier: AGPL-3.0-or-later
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.github.muelli.synctang"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.github.muelli.synctang"

        // minSdk 31: KeyProperties.PURPOSE_AGREE_KEY, which the whole
        // design rests on, was added in API 31. There is no fallback
        // worth having: without it the key holder's scalar would have
        // to leave the Keystore, which is exactly what this app exists
        // to avoid.
        minSdk = 31
        targetSdk = 34

        // versionCode must stay monotonic even when built from a source
        // tarball with no .git directory, which is how F-Droid builds.
        // Preference order: git history, then a committed VERSION file
        // at the repository root, then fail loudly. There is
        // deliberately no numeric default: shipping 1 because git
        // happened to be absent silently breaks updates for everyone
        // who already installed the app.
        val repoRoot = rootProject.projectDir.parentFile

        val gitVersionCount = try {
            providers.exec {
                commandLine("git", "rev-list", "--count", "HEAD")
                workingDir = repoRoot
                isIgnoreExitValue = true
            }.standardOutput.asText.get().trim().toIntOrNull()
        } catch (e: Exception) {
            null
        }

        val versionFile = repoRoot.resolve("VERSION")
        val fileVersionCount = if (versionFile.exists()) {
            versionFile.readText().trim().removePrefix("v").removeSuffix("-dirty").toIntOrNull()
        } else {
            null
        }

        versionCode = gitVersionCount ?: fileVersionCount ?: error(
            "Cannot determine versionCode: no git history and no VERSION file at the " +
                "repository root. Write the build's version number to VERSION."
        )
        versionName = versionCode.toString()

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables { useSupportLibrary = true }
    }

    buildTypes {
        // -Psynctang.noBiometric=true builds an app whose key is not
        // gated behind a biometric prompt, so that the unlock flow can
        // be driven end to end by an automated test on an emulator,
        // which cannot present a fingerprint. It is a testing
        // affordance and nothing else.
        //
        // It is honoured in debug builds only. The release branch below
        // hardcodes false regardless of what the property says, so
        // there is no combination of flags that produces a release APK
        // whose disk-unlocking key can be used without authentication.
        // The app checks BuildConfig.DEBUG as well at the point of use,
        // so removing that guard takes two deliberate edits, not one.
        val noBiometric = (findProperty("synctang.noBiometric") as String?)?.toBoolean() ?: false

        getByName("debug") {
            buildConfigField("boolean", "NO_BIOMETRIC", noBiometric.toString())
        }
        release {
            buildConfigField("boolean", "NO_BIOMETRIC", "false")
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    composeOptions {
        // Pinned against the Kotlin version in the root build file;
        // the Compose compiler refuses to run against a Kotlin it does
        // not know about, so the two move together or not at all.
        kotlinCompilerExtensionVersion = "1.5.10"
    }
    lint {
        abortOnError = false
    }
    packaging {
        // The gomobile .aar ships its own copies; nothing else in the
        // build supplies these, so a clash here means a duplicated
        // dependency rather than something to merge.
        resources.excludes += "META-INF/*.kotlin_module"
    }

    // The MR-1 fixed vectors are the same file mrcore's Go tests use.
    // Copying it into the test APK's assets, rather than transcribing
    // the numbers into Kotlin, is what makes the instrumented test a
    // check against the project's vectors instead of against itself.
    sourceSets {
        getByName("androidTest") {
            assets.srcDir(layout.buildDirectory.dir("generated/mr1Vectors"))
        }
    }
}

// Build the gomobile .aar that carries mrcore, the MR-1 wire format and
// the relay transport. The flags live in scripts/gomobile-bind.sh so
// that a local build, this task and CI all run the same command.
//
// Pass -Psynctang.goMobileTarget=android/amd64 to build only what an
// x86_64 emulator needs; the default builds every Android ABI, which is
// what a release must ship.
val goMobileAar = layout.projectDirectory.file("libs/synctang.aar")

tasks.register<Exec>("buildGoMobile") {
    val repoRoot = rootProject.projectDir.parentFile
    val target = (findProperty("synctang.goMobileTarget") as String?) ?: "android"

    inputs.dir(repoRoot.resolve("mobile"))
    inputs.dir(repoRoot.resolve("mrcore"))
    inputs.file(repoRoot.resolve("go.mod"))
    inputs.property("target", target)
    outputs.file(goMobileAar)

    workingDir = repoRoot
    commandLine("sh", "scripts/gomobile-bind.sh", goMobileAar.asFile.absolutePath, target)
}

val copyMr1Vectors by tasks.registering(Copy::class) {
    from(rootProject.projectDir.parentFile.resolve("testdata/mr1.json"))
    into(layout.buildDirectory.dir("generated/mr1Vectors"))
}

tasks.configureEach {
    if (name.startsWith("preBuild") || name.startsWith("compile") || name.startsWith("collect")) {
        dependsOn("buildGoMobile")
    }
    if (name.startsWith("generateAndroidTestDebugAssets") || name.startsWith("mergeDebugAndroidTestAssets")) {
        dependsOn(copyMr1Vectors)
    }
}

dependencies {
    implementation(fileTree("libs") { include("*.jar", "*.aar") })

    implementation("androidx.core:core-ktx:1.12.0")
    // BiometricPrompt needs a FragmentActivity, so MainActivity is one.
    implementation("androidx.fragment:fragment-ktx:1.6.2")
    implementation("androidx.activity:activity-compose:1.8.2")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.7.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.7.0")
    implementation(platform("androidx.compose:compose-bom:2024.02.02"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")

    // BiometricPrompt, the user-visible half of the gate in front of
    // the Keystore key.
    implementation("androidx.biometric:biometric:1.1.0")

    // EncryptedSharedPreferences for the pairing record. Nothing secret
    // is in there (the secret never leaves the Keystore), but the
    // machine's identity should not be trivially rewritable by another
    // app on a rooted phone.
    implementation("androidx.security:security-crypto:1.1.0-alpha06")

    // QR pairing: CameraX for the preview and frame analysis, ZXing
    // (Apache-2.0, no Play Services, no ML Kit) for the decoding.
    val cameraxVersion = "1.3.2"
    implementation("androidx.camera:camera-core:$cameraxVersion")
    implementation("androidx.camera:camera-camera2:$cameraxVersion")
    implementation("androidx.camera:camera-lifecycle:$cameraxVersion")
    implementation("androidx.camera:camera-view:$cameraxVersion")
    implementation("com.google.zxing:core:3.5.3")

    testImplementation("junit:junit:4.13.2")
    // Compose UI tests, for the keyboard navigation the desktop-mode
    // support rests on: focus order and key handling are only really
    // checkable by pressing the keys.
    androidTestImplementation(platform("androidx.compose:compose-bom:2024.02.02"))
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test:runner:1.5.2")
    androidTestImplementation("androidx.test:rules:1.5.0")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
