// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/**
 * The one switch that takes the biometric gate off the key, so that
 * the unlock flow can be driven end to end by an automated test.
 *
 * No emulator can present a fingerprint, and a JUnit test cannot tap
 * "confirm" on a prompt, so every automated check of the unlock path
 * stopped at the moment the gate appeared. That left the part most
 * likely to be wrong, the exchange with the machine, reachable only by
 * a human with a phone in their hand. Three real bugs in that exchange
 * were found that way in one evening, which is three more than the
 * test suite found in weeks.
 *
 * Two separate things have to be true for this to be on, and neither
 * can be flipped by accident:
 *
 *  - the build has to be a debug build (release hardcodes NO_BIOMETRIC
 *    to false in build.gradle.kts, whatever the property says), and
 *  - it has to have been built with -Psynctang.noBiometric=true.
 *
 * The key itself also lives under a different alias in this mode, so a
 * test key and a real one can never be confused for one another: they
 * are different keys with different public points, and a machine
 * enrolled against one will refuse the other.
 */
object TestMode {

    /** Whether the biometric gate is off. False in any release build. */
    val noBiometric: Boolean
        get() = BuildConfig.DEBUG && BuildConfig.NO_BIOMETRIC

    /**
     * The Keystore alias to use. Deliberately not the same in both
     * modes: an ungated key must never be usable by a build that
     * believes it is gated, and the only way to guarantee that is for
     * them not to be the same key.
     */
    fun aliasFor(noBiometric: Boolean): String =
        if (noBiometric) "${KeyHolderKey.DEFAULT_ALIAS}-no-biometric" else KeyHolderKey.DEFAULT_ALIAS
}

/**
 * A gate that never asks. Used only when [TestMode.noBiometric] is on,
 * where the Keystore key has no authentication requirement for it to
 * satisfy; with a gated key this would simply make the ECDH fail.
 */
object OpenGate : BiometricGate {
    override suspend fun authenticate(title: String, subtitle: String): AuthOutcome = AuthOutcome.SUCCEEDED
}
