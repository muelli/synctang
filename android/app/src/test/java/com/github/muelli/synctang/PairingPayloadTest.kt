// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * What the pairing screen accepts. A QR code is a convenience, not a
 * security boundary (the dial verifies the machine's identity
 * cryptographically), but accepting a malformed device ID would mean
 * an unlock attempt that fails for no visible reason, so the parsing
 * is worth pinning down.
 */
class PairingPayloadTest {

    private val id = "ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-4567ABC-DEFGHIJ-KLMNOPQ-RSTUVWX"

    @Test
    fun acceptsTheUriFormWithAName() {
        val machine = PairingPayload.parse("synctang://pair?machine=$id&name=study%20desktop")
        assertEquals(id, machine?.deviceId)
        assertEquals("study desktop", machine?.name)
    }

    @Test
    fun acceptsABareDeviceId() {
        assertEquals(id, PairingPayload.parse(id)?.deviceId)
    }

    @Test
    fun acceptsADeviceIdWithoutDashesAndInLowerCase() {
        val compact = id.replace("-", "").lowercase()
        assertEquals(id, PairingPayload.parse(compact)?.deviceId)
    }

    @Test
    fun rejectsAnythingThatIsNotADeviceId() {
        assertNull(PairingPayload.parse(""))
        assertNull(PairingPayload.parse("hello"))
        assertNull(PairingPayload.parse("synctang://pair?machine=nonsense"))
        // One character short.
        assertNull(PairingPayload.parse(id.replace("-", "").dropLast(1)))
        // The base32 alphabet has no 0, 1, 8 or 9.
        assertNull(PairingPayload.parse(id.replace("-", "").replaceFirst("A", "0")))
    }
}
