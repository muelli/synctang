// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The synctang Android key holder. android/wp0-spike/ is a separate,
// self-contained Gradle build (the WP0 feasibility spike, kept for the
// record) and is deliberately not included here.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}
rootProject.name = "synctang"
include(":app")
