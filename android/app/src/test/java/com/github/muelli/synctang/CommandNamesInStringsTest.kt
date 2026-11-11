// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The programs are called synctang-unlocker and synctang-keyholder, and every
 * text that tells a person to type one has to say so. This reads the shipped
 * strings, so a command added later under an old name is caught.
 */
class CommandNamesInStringsTest {

    private val strings = File("src/main/res/values/strings.xml").readText()

    @Test
    fun noCommandIsNamedWithoutThePrefix() {
        val bare = Regex("(?<![-\\w])(unlocker|keyholder) (enrol|pair|open|status|agent|unlock|init|export-pubkey)\\b")
        assertEquals(emptyList<String>(), bare.findAll(strings).map { it.value }.toList())
    }

    // "enrol --remove" was split into revoke and rotate; a text that still sends a person to it sends them to
    // a flag the program no longer has.
    @Test
    fun theRevokeCommandsAreTheCurrentOnes() {
        assertTrue(strings.contains("synctang-unlocker revoke "))
        assertEquals(emptyList<String>(), Regex("enrol --remove|--remove-keyslot").findAll(strings).map { it.value }.toList())
    }
}
