// SPDX-License-Identifier: AGPL-3.0-or-later
package org.synctang.wp0spike

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec
import javax.crypto.KeyAgreement

/**
 * WP0 feasibility spike (T0.1).
 *
 * Question: does Android Keystore ECDH accept a manually constructed P-256
 * public key (raw x, y coordinates, not produced by a KeyPairGenerator) as
 * the peer key, and does doPhase()/generateSecret() return the correct raw
 * x-coordinate of s.X rather than something KDF'd?
 *
 * This matters because the mrcore protocol builds the peer point X by curve
 * addition in software (X = C + E) and never through a KeyPairGenerator. If
 * the Keystore rejects a manually constructed point, or returns something
 * other than the raw x-coordinate, the whole approach of holding the phone's
 * private scalar in the Keystore/StrongBox and doing ECDH against X does not
 * work, and the design falls back to a software-encrypted key instead (see
 * docs/wp0-keystore-ecdh.md).
 *
 * See docs/wp0-keystore-ecdh.md for the full write-up, exact versions used,
 * and the actual byte values from a real run.
 */
@RunWith(AndroidJUnit4::class)
class KeystoreEcdhSpikeTest {

    private val alias = "wp0-spike-keystore-key"

    @Test
    fun keystoreEcdhAcceptsManuallyConstructedPeerPointAndReturnsRawXCoordinate() {
        // Step 1: software EC keypair e/E, NOT from the AndroidKeyStore
        // provider. Exportable, e known to this test.
        val softwareKpg = KeyPairGenerator.getInstance("EC")
        softwareKpg.initialize(ECGenParameterSpec("secp256r1"))
        val softwareKeyPair = softwareKpg.generateKeyPair()
        val softwarePrivate = softwareKeyPair.private
        val softwarePublic = softwareKeyPair.public as ECPublicKey

        // Step 2: pull E's raw (x, y) out and rebuild it as a brand new
        // PublicKey via ECPublicKeySpec. This simulates the arbitrary
        // curve point the machine side builds by point addition: it is
        // never the object the KeyPairGenerator produced.
        val params = softwarePublic.params
        val rebuiltPoint = ECPoint(softwarePublic.w.affineX, softwarePublic.w.affineY)
        val keyFactory = KeyFactory.getInstance("EC")
        val rebuiltPeerPublic = keyFactory.generatePublic(ECPublicKeySpec(rebuiltPoint, params))

        // Step 3: Keystore EC keypair s/S, purpose KEY_AGREEMENT,
        // setUserAuthenticationRequired(false) (biometric gating is a
        // separate manual real-device step, see the doc). Try StrongBox
        // first, fall back to non-StrongBox on StrongBoxUnavailableException
        // and log which path was used.
        val keyStore = KeyStore.getInstance("AndroidKeyStore")
        keyStore.load(null)
        if (keyStore.containsAlias(alias)) {
            keyStore.deleteEntry(alias)
        }

        val keystoreKpg = KeyPairGenerator.getInstance(
            KeyProperties.KEY_ALGORITHM_EC,
            "AndroidKeyStore"
        )

        fun buildSpec(strongBox: Boolean): KeyGenParameterSpec =
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_AGREE_KEY)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setUserAuthenticationRequired(false)
                .setIsStrongBoxBacked(strongBox)
                .build()

        var strongBoxUnavailableThrown: Boolean
        var strongBoxBacked: Boolean
        try {
            keystoreKpg.initialize(buildSpec(true))
            keystoreKpg.generateKeyPair()
            strongBoxUnavailableThrown = false
            strongBoxBacked = true
            Log.i(TAG, "StrongBox-backed key generated on first attempt")
        } catch (e: StrongBoxUnavailableException) {
            strongBoxUnavailableThrown = true
            strongBoxBacked = false
            Log.i(TAG, "StrongBoxUnavailableException thrown (expected on the emulator): ${e.message}")
            keyStore.load(null)
            if (keyStore.containsAlias(alias)) {
                keyStore.deleteEntry(alias)
            }
            keystoreKpg.initialize(buildSpec(false))
            keystoreKpg.generateKeyPair()
        }
        Log.i(TAG, "strongBoxUnavailableThrown=$strongBoxUnavailableThrown strongBoxBacked=$strongBoxBacked")

        keyStore.load(null)
        val keystorePrivate = keyStore.getKey(alias, null) as PrivateKey
        val keystorePublic = keyStore.getCertificate(alias).publicKey

        // Step 4: A = x(s . E') computed inside the Keystore/StrongBox,
        // against the manually constructed peer point E' from step 2.
        val agreementA = KeyAgreement.getInstance("ECDH")
        agreementA.init(keystorePrivate)
        agreementA.doPhase(rebuiltPeerPublic, true)
        val secretA = agreementA.generateSecret()

        // Step 5: B = x(e . S) computed entirely in software (independent
        // ECDH, no Keystore involved), against the Keystore's own public
        // key S. By commutativity this is the same point as A:
        // x(s.e.G) == x(e.s.G). This computation is also, by construction,
        // an independent software recomputation of the x-coordinate, which
        // is exactly the "is this really x(s.X) and not something else"
        // check from the test design; no further computation is needed for
        // that, per the test design notes.
        val agreementB = KeyAgreement.getInstance("ECDH")
        agreementB.init(softwarePrivate)
        agreementB.doPhase(keystorePublic, true)
        val secretB = agreementB.generateSecret()

        val hexA = secretA.joinToString("") { "%02x".format(it) }
        val hexB = secretB.joinToString("") { "%02x".format(it) }
        Log.i(TAG, "secretA(hex)=$hexA len=${secretA.size}")
        Log.i(TAG, "secretB(hex)=$hexB len=${secretB.size}")

        assertEquals(
            "generateSecret() must return the raw 32-byte P-256 x-coordinate, not a KDF'd value",
            32,
            secretA.size
        )
        assertArrayEquals(
            "A (Keystore ECDH: private key s in Keystore/StrongBox, peer key E' " +
                "manually constructed from raw coordinates via ECPublicKeySpec) must equal " +
                "B (independent software-only ECDH: private key e, peer key S read from the " +
                "Keystore certificate). Both equal x(s.e.G) = x(e.s.G) by commutativity. This " +
                "equality is what proves the Keystore accepted the manually-constructed point " +
                "and returned the correct raw x-coordinate of s.E, not a KDF'd value and not " +
                "some other point.",
            secretA,
            secretB
        )

        keyStore.deleteEntry(alias)
    }

    companion object {
        private const val TAG = "WP0Spike"
    }
}
