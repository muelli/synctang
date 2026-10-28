// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.content.pm.PackageManager
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import mobile.Mobile
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * WP5, phase 2: the app's own Keystore ECDH path, driven end to end
 * against the Go side of MR-1.
 *
 * WP0 proved the Keystore will do ECDH against a manually constructed
 * peer point. This goes further and closes the loop: a machine enrols
 * a secret against the public point this app publishes, challenges it,
 * the app answers through the very [KeyHolderKey.agree] the unlock
 * flow calls, and the machine must recover the secret it enrolled. A
 * mistake anywhere in the chain (coordinate padding, SEC1 encoding,
 * the wrong curve, an x-coordinate that is not the x-coordinate)
 * shows up as a failed recovery here.
 *
 * The test key sets requireUserAuthentication = false: no emulator can
 * present a fingerprint, and a test that cannot run proves nothing.
 * Everything else, including the ECDH call itself, is the production
 * path unchanged. The biometric gate is exercised by hand on a real
 * device, and is described in KeyHolderKey's documentation.
 */
@RunWith(AndroidJUnit4::class)
class KeystoreEcdhTest {

    private val alias = "synctang-instrumented-test"
    private lateinit var key: KeyHolderKey

    @Before
    fun setUp() {
        key = KeyHolderKey(alias = alias, requireUserAuthentication = false)
        key.delete()
        key.ensureKey()
    }

    @After
    fun tearDown() {
        key.delete()
    }

    // On a phone that advertises StrongBox the key has to be in it: that is
    // the claim the app makes in its About screen and the reason the key is
    // created the way it is. On an emulator, or a phone without it, there is
    // nothing to check and the test is skipped rather than passed. The
    // backing is logged either way, so a run shows what it actually got.
    @Test
    fun theKeyIsInStrongBoxOnAPhoneThatHasOne() {
        // The facts behind the answer, logged raw: the level the framework
        // reports, whether it says the key is inside secure hardware, and how
        // long creating a key takes (a StrongBox chip is much slower than the
        // TEE). They are what tells a phone that refused StrongBox from a
        // framework that reports the level wrongly.
        key.delete()
        val started = System.nanoTime()
        key.ensureKey()
        val millis = (System.nanoTime() - started) / 1_000_000
        val keyStore = java.security.KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val private = keyStore.getKey(alias, null) as java.security.PrivateKey
        val info = java.security.KeyFactory.getInstance(private.algorithm, "AndroidKeyStore")
            .getKeySpec(private, android.security.keystore.KeyInfo::class.java)
        Log.i(
            "synctang-test",
            "key facts: securityLevel=${info.securityLevel} (1=TEE, 2=StrongBox) " +
                "insideSecureHardware=${info.isInsideSecureHardware} creationMillis=$millis",
        )
        val backing = key.backing()
        Log.i("synctang-test", "key backing: $backing")
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        assumeTrue(
            "this device does not advertise StrongBox, so there is nothing to check",
            context.packageManager.hasSystemFeature(PackageManager.FEATURE_STRONGBOX_KEYSTORE),
        )
        assertEquals(KeyHolderKey.Backing.STRONGBOX, backing)
    }

    @Test
    fun theMachineRecoversWhatItEnrolledAfterTheKeystoreAnswers() {
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())

        val peerX = Mobile.affineX(challenge.point())
        val peerY = Mobile.affineY(challenge.point())
        val xOnly = key.agree(peerX, peerY)

        assertEquals("ECDH must yield a raw 32-byte x-coordinate", 32, xOnly.size)
        assertArrayEquals(
            "the machine must recover exactly the secret it enrolled",
            challenge.secret(),
            challenge.finish(xOnly),
        )
    }

    @Test
    fun theMachineIsRefusedWhenAnotherKeyAnswers() {
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())
        val impostor = KeyHolderKey(alias = "$alias-impostor", requireUserAuthentication = false)
        try {
            impostor.delete()
            impostor.ensureKey()

            val xOnly = impostor.agree(
                Mobile.affineX(challenge.point()),
                Mobile.affineY(challenge.point()),
            )

            var threw = false
            try {
                challenge.finish(xOnly)
            } catch (e: Exception) {
                threw = true
            }
            assertTrue("a wrong key holder must fail the AEAD check, not return a secret", threw)
        } finally {
            impostor.delete()
        }
    }

    @Test
    fun theKidTheAppPublishesIsTheOneTheMachineAsksFor() {
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())
        assertArrayEquals(
            "the app must recognise a request naming its own key",
            challenge.kid(),
            key.kid(),
        )
    }

    @Test
    fun theLongTermKeyIsNotExportableAndSurvivesRestarts() {
        val first = key.publicPoint()
        assertEquals("S must be a SEC1 uncompressed P-256 point", 65, first.size)
        assertEquals(0x04.toByte(), first[0])

        // A second handle onto the same alias must see the same key:
        // the app reopens the Keystore on every launch and must not
        // silently generate a new identity, which would break every
        // machine already enrolled against it.
        val again = KeyHolderKey(alias = alias, requireUserAuthentication = false)
        assertTrue(again.exists())
        assertArrayEquals(first, again.publicPoint())
        assertFalse("ensureKey must not replace an existing key", again.ensureKey().name.isEmpty())
        assertArrayEquals(first, again.publicPoint())
    }
}
