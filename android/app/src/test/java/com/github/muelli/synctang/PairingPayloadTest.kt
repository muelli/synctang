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

    // What "synctang-unlocker pair" prints under its QR code, for a camera that cannot
    // scan: the link the code carries, pasted into the device ID field. This one
    // is verbatim from a real run. A device ID typed on its own carries no code,
    // so it only remembers the machine; the link is what enrols the phone.
    @Test
    fun aPastedPairingLinkCarriesTheMachineItsNameAndTheOneTimeCode() {
        val link = "synctang://pair?machine=H5APP3N-YIQZANN-4LPEVJF-K56O4RF-JH3AWOJ-THBBOI6-BHKBC7J-QGRKIAX" +
            "&name=synctang-local&psk=KMAXCD654UPFOAB2ICWOPBDUUQ"
        val machine = PairingPayload.parse(link)
        assertEquals("H5APP3N-YIQZANN-4LPEVJF-K56O4RF-JH3AWOJ-THBBOI6-BHKBC7J-QGRKIAX", machine?.deviceId)
        assertEquals("synctang-local", machine?.name)
        assertEquals("KMAXCD654UPFOAB2ICWOPBDUUQ", PairingPayload.pairingCode(link))
        assertTrue(planPairing(link) is PairingPlan.Enrol)
    }

    @Test
    fun aDeviceIdTypedOnItsOwnOnlyRemembersTheMachine() {
        assertTrue(planPairing(id) is PairingPlan.Remember)
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

    // A machine with its own discovery server says so in the link, so the phone looks it up
    // where the machine announces. The link is what "synctang-unlocker pair" prints, with the URL
    // escaped as java.net.URLEncoder does. The query of the URL (an id= that pins the server's
    // certificate) travels inside it.
    @Test
    fun aLinkCanCarryTheMachinesDiscoveryServer() {
        val link = "synctang://pair?machine=$id&name=study" +
            "&discovery=https%3A%2F%2Fdisco.example.net%3A8443%2Fv2%2F%3Fid%3DP56IOI7-MZJNU2Y&psk=KMAXCD654UPFOAB2ICWOPBDUUQ"
        val machine = PairingPayload.parse(link)
        assertEquals("https://disco.example.net:8443/v2/?id=P56IOI7-MZJNU2Y", machine?.discovery)
        val plan = planPairing(link) as PairingPlan.Enrol
        assertEquals("https://disco.example.net:8443/v2/?id=P56IOI7-MZJNU2Y", plan.machine.discovery)
    }

    @Test
    fun aLinkWithoutItMeansThePublicServers() {
        val machine = PairingPayload.parse("synctang://pair?machine=$id&name=study&psk=ABC")
        assertEquals("", machine?.discovery)
        assertEquals("", PairingPayload.parse(id)?.discovery)
    }

    // Only the machine joins a relay; a phone learns it from discovery. A link that names one
    // (an older machine's) is refused like any other setting this app does not carry, and says so.
    @Test
    fun aLinkWithARelaySettingIsRefusedAsAnUnknownParameter() {
        val link = "synctang://pair?machine=$id&name=study&psk=ABC&relay=https%3A%2F%2Fp.example.net%2Fendpoint"
        val plan = planPairing(link)
        assertTrue("got $plan", plan is PairingPlan.UnknownParameters)
        assertEquals(listOf("relay"), (plan as PairingPlan.UnknownParameters).names)
    }

    // A setting this app does not know is not ignorable: ignoring, say, a future "relay-only=1"
    // would pair the phone against servers the owner chose not to use.
    @Test
    fun aLinkWithAParameterTheAppDoesNotKnowIsRefused() {
        val link = "synctang://pair?machine=$id&name=study&psk=ABC&tor=1&relay-only=yes"
        val plan = planPairing(link)
        assertTrue("got $plan", plan is PairingPlan.UnknownParameters)
        assertEquals(listOf("tor", "relay-only"), (plan as PairingPlan.UnknownParameters).names)
    }

    @Test
    fun theParametersTheAppKnowAreNotUnknown() {
        val link = "synctang://pair?machine=$id&name=study&psk=ABC&discovery=https%3A%2F%2Fd.example.net%2Fv2%2F"
        assertTrue(planPairing(link) is PairingPlan.Enrol)
    }

    @Test
    fun theNamesOfUnknownParametersAreMadeSafeToShow() {
        val link = "synctang://pair?machine=$id&" + "x".repeat(200) + "=1&a%0Ab%20c=2"
        val names = (planPairing(link) as PairingPlan.UnknownParameters).names
        assertTrue(names.all { it.length <= 24 })
        assertTrue(names.all { name -> name.all { it.isLetterOrDigit() || it in "_.-" } })
    }

    @Test
    fun aSettingThatIsNotAUrlIsRefused() {
        for (bad in listOf("discovery=ftp%3A%2F%2Fx.example.net%2F", "discovery=disco.example.net")) {
            val plan = planPairing("synctang://pair?machine=$id&psk=ABC&$bad")
            assertTrue("$bad gave $plan", plan is PairingPlan.BadSetting)
            assertEquals(bad.substringBefore('='), (plan as PairingPlan.BadSetting).name)
        }
    }

    // Plain http is for a lookup that says it is meant: the Go side refuses an http discovery
    // server without ?insecure at unlock time, so the app refuses it where it is scanned.
    @Test
    fun aDiscoveryServerOverPlainHttpNeedsInsecure() {
        fun plan(discovery: String) =
            planPairing("synctang://pair?machine=$id&psk=ABC&discovery=" + java.net.URLEncoder.encode(discovery, "UTF-8"))
        assertTrue(plan("http://10.0.0.6:8443/v2/") is PairingPlan.PlainHttpDiscovery)
        assertTrue(plan("HTTP://10.0.0.6:8443/v2/?x=1") is PairingPlan.PlainHttpDiscovery)
        assertTrue(plan("http://10.0.0.6:8443/v2/?insecure=false") is PairingPlan.PlainHttpDiscovery)
        assertTrue(plan("http://10.0.0.6:8443/v2/?insecure") is PairingPlan.Enrol)
        assertTrue(plan("http://10.0.0.6:8443/v2/?x=1&insecure=true") is PairingPlan.Enrol)
        assertTrue(plan("https://10.0.0.6:8443/v2/") is PairingPlan.Enrol)
        assertTrue(plan("https://10.0.0.6:8443/v2/?id=P56IOI7-MZJNU2Y") is PairingPlan.Enrol)
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
                "synctang-unlocker enrol --device /dev/sdXN --name my-machine \\\n" +
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
