# SPDX-License-Identifier: AGPL-3.0-or-later
#
# The gomobile binding is reached through JNI, so its classes and the
# go.Seq runtime are looked up by name at runtime and nothing in the
# Java sources references them in a way a shrinker can follow.
-keep class mobile.** { *; }
-keep class go.** { *; }
