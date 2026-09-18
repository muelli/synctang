// SPDX-License-Identifier: AGPL-3.0-or-later
//
// WP0 feasibility spike: throwaway module, not wired into the future
// android/app/ that WP5 will start fresh. Kept for the record only.
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
rootProject.name = "wp0-spike"
include(":app")
