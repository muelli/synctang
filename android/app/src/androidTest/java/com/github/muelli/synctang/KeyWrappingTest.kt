// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.security.keystore.KeyInfo
import android.security.keystore.UserNotAuthenticatedException
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.security.KeyFactory
import java.security.KeyStore
import mobile.Mobile
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The key holder's key on a real device: a hardware RSA key that only unwraps, the
 * scalar made in Go and kept wrapped, one use at a time.
 *
 * Most of these use a key that does not require a fingerprint, because no emulator
 * can present one and a test that cannot run proves nothing; everything else about
 * the key is identical. [aKeyThatRequiresAFingerprintRefusesWithoutOne] is the one
 * that shows the hardware gate itself, and needs no finger to do it: it checks that
 * the unwrapping is REFUSED when nobody has touched the sensor.
 */
@RunWith(AndroidJUnit4::class)
class KeyWrappingTest {

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val directory = File(context.cacheDir, "key-wrapping-test")
    private val alias = "synctang-instrumented-test"
    private val tag = "synctang-test"
    private lateinit var key: KeyHolderKey
    private val others = mutableListOf<KeyHolderKey>()

    @Before
    fun setUp() {
        directory.deleteRecursively()
        key = KeyHolderKey(directory, alias, requireUserAuthentication = false)
        key.delete()
        key.ensureKey()
    }

    @After
    fun tearDown() {
        key.delete()
        others.forEach { it.delete() }
        directory.deleteRecursively()
    }

    private fun scalarOf(k: KeyHolderKey): ByteArray = k.unwrap(k.prepareUnwrap())

    // The app tells the person where the hardware half of their key lives, so it
    // must never say hardware when Android says otherwise. The raw facts are logged.
    @Test
    fun theBackingIsWhatAndroidSays() {
        key.delete()
        val started = System.nanoTime()
        key.ensureKey()
        val millis = (System.nanoTime() - started) / 1_000_000
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val private = keyStore.getKey(alias, null) as java.security.PrivateKey
        val info = KeyFactory.getInstance(private.algorithm, "AndroidKeyStore")
            .getKeySpec(private, KeyInfo::class.java)
        Log.i(
            tag,
            "wrapping key: algorithm=${private.algorithm} securityLevel=${info.securityLevel} " +
                "insideSecureHardware=${info.isInsideSecureHardware} creationMillis=$millis " +
                "reportedBacking=${key.backing()}",
        )
        assertEquals(KeyHolderKey.classify(info.securityLevel), key.backing())
        if (!info.isInsideSecureHardware) {
            assertEquals(KeyHolderKey.Backing.SOFTWARE, key.backing())
        }
    }

    // On a phone with secure hardware the wrapping key lives there: that is the
    // reason for the design. Not asserted on a device without any (an emulator).
    @Test
    fun onAPhoneWithSecureHardwareTheWrappingKeyIsNotInSoftware() {
        assumeTrue("no secure hardware to hold it", key.backing() != KeyHolderKey.Backing.SOFTWARE)
        assertTrue(key.backing() == KeyHolderKey.Backing.STRONGBOX || key.backing() == KeyHolderKey.Backing.TEE)
    }

    @Test
    fun theKeyIsThereAfterARestartAndHasTheSamePublicPoint() {
        val before = key.publicPoint()
        val again = KeyHolderKey(directory, alias, requireUserAuthentication = false)
        assertTrue(again.exists())
        assertArrayEquals(before, again.publicPoint())
        assertEquals(65, before.size)
        assertEquals(4.toByte(), before[0])
        assertArrayEquals(java.security.MessageDigest.getInstance("SHA-256").digest(before), again.kid())
    }

    // The whole point, end to end: a machine enrols against the public point, the
    // unwrapped scalar answers its challenge through the Go side, and the machine
    // recovers the very secret it enrolled. A mistake anywhere (the wrapping, the
    // OAEP parameters, the scalar, the curve) shows up here as a failed recovery.
    @Test
    fun theMachineRecoversWhatItEnrolledWhenTheKeyAnswers() {
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())
        val started = System.nanoTime()
        val scalar = scalarOf(key)
        val unwrapMillis = (System.nanoTime() - started) / 1_000_000
        Log.i(tag, "unwrap took ${unwrapMillis} ms")
        try {
            assertEquals(32, scalar.size)
            assertArrayEquals(challenge.secret(), challenge.finishWithScalar(scalar.copyOf()))
        } finally {
            scalar.fill(0)
        }
    }

    @Test
    fun anotherKeyDoesNotRecoverTheSecret() {
        val other = KeyHolderKey(directory, "$alias-other", requireUserAuthentication = false).also {
            others += it
            it.delete()
            it.ensureKey()
        }
        assertFalse(java.util.Arrays.equals(key.publicPoint(), other.publicPoint()))
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())
        val scalar = scalarOf(other)
        try {
            val got = try {
                challenge.finishWithScalar(scalar.copyOf())
            } catch (e: Exception) {
                null
            }
            assertFalse("another key recovered the secret", got != null && got.contentEquals(challenge.secret()))
        } finally {
            scalar.fill(0)
        }
    }

    // At rest the scalar is only ever wrapped: neither of the two files holds it.
    @Test
    fun theFilesOnDiskHoldNoPlaintextScalar() {
        val scalar = scalarOf(key)
        try {
            for (file in directory.walkTopDown().filter { it.isFile }) {
                val bytes = file.readBytes()
                assertFalse(
                    "${file.name} contains the plaintext scalar",
                    bytes.toList().windowed(scalar.size).any { it == scalar.toList() },
                )
            }
            // And the wrapped file is a full RSA block, not something shorter.
            assertEquals(256, File(directory, "$alias.wrapped").length())
        } finally {
            scalar.fill(0)
        }
    }

    @Test
    fun aTamperedWrappedFileIsRefusedAndNeverUnwrapsToAnotherKey() {
        val file = File(directory, "$alias.wrapped")
        val bytes = file.readBytes()
        bytes[100] = (bytes[100].toInt() xor 1).toByte()
        file.writeBytes(bytes)
        try {
            val scalar = scalarOf(key)
            scalar.fill(0)
            fail("a tampered wrapped key was unwrapped")
        } catch (e: javax.crypto.BadPaddingException) {
            // expected
        } catch (e: java.security.GeneralSecurityException) {
            // also a refusal; the Keystore's own wording varies by version
        }
    }

    @Test
    fun deletingTheKeyRemovesBothHalves() {
        key.delete()
        assertFalse(key.exists())
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        assertFalse(keyStore.containsAlias(alias))
        assertEquals(emptyList<String>(), directory.list()?.toList() ?: emptyList<String>())
    }

    // The scalar is wrapped inside this process, by a provider that lives here, not
    // by the Keystore daemon, which would be sent a scalar it has no use for.
    @Test
    fun theScalarIsWrappedInThisProcessNotByTheKeystore() {
        val publicKey = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            .getCertificate(alias).publicKey
        val provider = Oaep.wrapCipher(publicKey).provider.name
        Log.i("KeyWrappingTest", "the wrapping cipher runs in provider $provider")
        assertFalse("wrapped by $provider", provider.startsWith("AndroidKeyStore"))
    }

    // A creation that stopped after the wrapped key never showed a public point, so
    // nothing is enrolled on it and it is made again, with a new public point.
    @Test
    fun anUnfinishedCreationIsMadeAgainAsANewOne() {
        val before = key.publicPoint()
        File(directory, "$alias.public").delete()
        assertFalse(key.exists())
        key.ensureKey()
        assertTrue(key.exists())
        assertNotEquals(before.toList(), key.publicPoint().toList())
    }

    // Anything else that is half there may be a key machines are enrolled on, and a
    // Keystore that fails to answer looks the same as one with no key. The app does
    // not delete it; the person decides, and delete() is their choice.
    @Test
    fun aMissingWrappedKeyIsNotClearedBehindThePersonsBack() {
        val before = key.publicPoint()
        File(directory, "$alias.wrapped").delete()
        try {
            key.ensureKey()
            fail("expected the key to need replacing")
        } catch (e: KeyNeedsReplacing) {
            assertEquals(KeyProblem.FILES_DAMAGED, e.problem)
        }
        assertArrayEquals(before, key.publicPoint())
        assertTrue(KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.containsAlias(alias))
    }

    @Test
    fun aMissingHardwareKeyIsNotClearedBehindThePersonsBack() {
        val before = key.publicPoint()
        KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(alias)
        try {
            key.ensureKey()
            fail("expected the key to need replacing")
        } catch (e: KeyNeedsReplacing) {
            assertEquals(KeyProblem.HARDWARE_HALF_MISSING, e.problem)
        }
        assertArrayEquals("the wrapped half was kept", before, key.publicPoint())

        // The person's choice: delete, and the next ensureKey makes a new one.
        key.delete()
        key.ensureKey()
        assertTrue(key.exists())
        assertNotEquals(before.toList(), key.publicPoint().toList())
    }

    @Test
    fun theHardwareHalfWithoutTheFilesIsMadeAgainToo() {
        directory.deleteRecursively()
        assertFalse(key.exists())
        key.ensureKey()
        assertTrue(key.exists())
        val challenge = Mobile.enrolAndChallenge(key.publicPoint())
        val scalar = scalarOf(key)
        try {
            assertArrayEquals(challenge.secret(), challenge.finishWithScalar(scalar.copyOf()))
        } finally {
            scalar.fill(0)
        }
    }

    // The hardware gate, shown without a finger: a key that requires a strong
    // biometric per use REFUSES to unwrap when nobody has authenticated the
    // operation. Not run on a phone with no biometric enrolled, where such a key
    // cannot be made at all.
    @Test
    fun aKeyThatRequiresAFingerprintRefusesWithoutOne() {
        val gated = KeyHolderKey(directory, "$alias-gated", requireUserAuthentication = true).also { others += it }
        try {
            gated.delete()
            gated.ensureKey()
        } catch (e: BiometricEnrolmentRequired) {
            assumeTrue("no biometric is enrolled on this device", false)
        }
        val cipher = gated.prepareUnwrap()
        try {
            val scalar = gated.unwrap(cipher)
            scalar.fill(0)
            fail("the key was unwrapped although no fingerprint was presented")
        } catch (e: KeyRefusedFingerprint) {
            // The platform's own error, found by its code through reflection: this is
            // the one place that shows the real class has the accessor on this Android.
            assertTrue(e.cause!!.isUserNotAuthenticated())
            Log.i(tag, "refused without a fingerprint, as ${generateSequence<Throwable>(e) { it.cause }.map { it.javaClass.name }.toList()}")
            return
        } catch (e: Exception) {
            // The Keystore reports "Key user not authenticated"; which exception
            // carries it differs between Android versions (here an
            // IllegalBlockSizeException wrapping a KeyStoreException), so the whole
            // cause chain is read, as for the biometric enrolment error.
            val chain = generateSequence<Throwable>(e) { it.cause }.toList()
            assertTrue(
                "refused, but not for want of a fingerprint: $chain",
                chain.any { it is UserNotAuthenticatedException || it.message?.contains("not authenticated", ignoreCase = true) == true },
            )
            Log.i(tag, "refused without a fingerprint, as it must: ${chain.last().message}")
        }
    }
}
