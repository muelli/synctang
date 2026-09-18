// SPDX-License-Identifier: AGPL-3.0-or-later
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "org.synctang.wp0spike"
    compileSdk = 34

    defaultConfig {
        applicationId = "org.synctang.wp0spike"
        // minSdk 31: KeyProperties.PURPOSE_AGREE_KEY (used below) needs API 31.
        // This spike only ever runs on the API 34 emulator image, so this is
        // not a real product minSdk decision, just what this test needs.
        minSdk = 31
        targetSdk = 34
        versionCode = 1
        versionName = "0.1"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    androidTestImplementation("androidx.test.ext:junit:1.1.5")
    androidTestImplementation("androidx.test:runner:1.5.2")
}
