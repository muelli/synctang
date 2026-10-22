// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
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

/**
 * The enrolment screen's whole job is to hand over a command that can
 * be pasted and will work. It got the flag wrong: it printed
 * --transport-id, which sets the *machine's* own transport identity,
 * where it meant --recipient-transport-id, the flag that records who
 * is allowed to unlock. Pasting it would have told the machine to
 * adopt the phone's Device ID as its own and to enrol a recipient
 * with no transport identity at all, so the phone would then have
 * been refused as an unauthorized peer, with nothing on either screen
 * to suggest the command had been the problem.
 *
 * It then told people to keep the LUKS passphrase in a file
 * (--existing-passphrase-file /path/to/passphrase), which this project
 * never does: the passphrase is read into a shell variable and handed
 * over through a process substitution, as the README shows, so it
 * never touches a disk. And it passed --name phone, but enrol's --name
 * is the machine's own name, so the first enrolment would have
 * labelled the machine "phone".
 */
class EnrolCommandTest {

    private val deviceId = "ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-4567ABC-DEFGHIJ-KLMNOPQ-RSTUVWX"

    private val command = enrolCommand(publicKeyHex = "04aabb", deviceId = deviceId)

    @Test
    fun isExactlyTheReadmeForm() {
        assertEquals(
            "read -rsp 'LUKS passphrase: ' PASS\n" +
                "unlocker enrol --device /dev/sdXN --name my-machine \\\n" +
                "  --pubkey 04aabb \\\n" +
                "  --recipient-transport-id $deviceId \\\n" +
                "  --existing-passphrase-file <(printf %s \"\$PASS\")",
            command,
        )
    }

    @Test
    fun namesTheRecipientFlagAndNotTheMachinesOwn() {
        assertTrue(
            "the phone's Device ID must be given as the recipient's: $command",
            command.contains("--recipient-transport-id ABCDEFG-"),
        )
        assertFalse(
            "--transport-id sets the machine's own identity, not the recipient's: $command",
            Regex("(^|\\s)--transport-id\\s").containsMatchIn(command),
        )
    }

    @Test
    fun neverPointsThePassphraseAtAFileOnDisk() {
        val given = Regex("--existing-passphrase-file\\s+(\\S+)").findAll(command).map { it.groupValues[1] }.toList()
        assertEquals("enrol needs the passphrase exactly once: $command", 1, given.size)
        assertTrue(
            "the passphrase must arrive through a process substitution, not a path: $command",
            given.single().startsWith("<("),
        )
        assertFalse(command.contains("/path/to"))
        assertFalse(Regex("passphrase[^ ]*\\.(txt|key)|/passphrase").containsMatchIn(command))
    }

    @Test
    fun readsThePassphraseWithoutEchoingIt() {
        assertTrue(command.lines().first() == "read -rsp 'LUKS passphrase: ' PASS")
    }

    @Test
    fun usesAPlaceholderDeviceRatherThanAnEllipsis() {
        assertTrue(command.contains("--device /dev/sdXN "))
        assertFalse(command.contains("/dev/..."))
    }

    @Test
    fun doesNotNameTheMachineAfterThePhone() {
        assertFalse(command.contains("--name phone"))
    }

    @Test
    fun carriesThePublicKey() {
        assertTrue(command.contains("--pubkey 04aabb"))
    }
}

/**
 * The one-time pairing code the machine shows alongside its Device ID.
 *
 * It is what lets the phone finish enrolment over the network instead
 * of a person copying a public key between two devices by hand, so a
 * payload that carries one has to be told apart from one that does not:
 * the older form, with no code in it, still has to work and still means
 * "remember this machine, enrol separately".
 */
class PairingCodeTest {

    @Test
    fun aPayloadWithACodeYieldsBothTheMachineAndTheCode() {
        val scanned = "synctang://pair?machine=ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-456ABCD-EFGHIJK-LMNOPQR-STUVWXY" +
            "&name=study&psk=ORSXG5BRGIZQABCDEFGHIJKLMN"

        val machine = PairingPayload.parse(scanned)
        assertNotNull("the machine must still parse", machine)
        assertEquals("study", machine!!.name)
        assertEquals("ORSXG5BRGIZQABCDEFGHIJKLMN", PairingPayload.pairingCode(scanned))
    }

    @Test
    fun aPayloadWithoutACodeHasNone() {
        val scanned = "synctang://pair?machine=ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-456ABCD-EFGHIJK-LMNOPQR-STUVWXY"
        assertNull(PairingPayload.pairingCode(scanned))
    }

    @Test
    fun abareDeviceIdHasNoCode() {
        assertNull(PairingPayload.pairingCode("ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-456ABCD-EFGHIJK-LMNOPQR-STUVWXY"))
    }

    @Test
    fun anEmptyCodeCountsAsAbsentRatherThanAsACodeThatCannotWork() {
        val scanned = "synctang://pair?machine=ABCDEFG-HIJKLMN-OPQRSTU-VWXYZ23-456ABCD-EFGHIJK-LMNOPQR-STUVWXY&psk="
        assertNull(PairingPayload.pairingCode(scanned))
    }
}
