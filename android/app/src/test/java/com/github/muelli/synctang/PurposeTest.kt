// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

/**
 * What a machine says in its greeting it will do with the answer, in words the person reads: for the purposes the
 * app knows, a string of its own, and for any other the machine's own word, passed on as an argument (cleaned and
 * cut by the library before it gets here), so that a purpose from a newer machine is still not an unlock.
 */
class PurposeTest {
    @Test
    fun theKnownPurposesHaveWordsOfTheirOwn() {
        val enrol = purposeLabel("enrol")
        val pair = purposeLabel("pair")
        val extract = purposeLabel("extract")
        assertEquals(R.string.purpose_enrol, enrol.res)
        assertEquals(R.string.purpose_pair, pair.res)
        assertEquals(R.string.purpose_extract, extract.res)
        assertEquals(setOf(R.string.purpose_enrol, R.string.purpose_pair, R.string.purpose_extract), setOf(enrol.res, pair.res, extract.res))
    }

    @Test
    fun anotherPurposeIsShownAsTheMachinePutItAndIsNotTakenForAnUnlock() {
        val other = purposeLabel("wipe it")
        assertEquals(R.string.purpose_other, other.res)
        assertEquals(listOf<Any>("wipe it"), other.args)
        assertNotEquals(R.string.purpose_enrol, other.res)
    }
}
