// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What to do about the two halves of the key, given what is there. The
 * danger is one particular mistake: the Keystore answering "no such key"
 * when it merely failed to answer (it reports most errors as absence), and
 * the app, taking that at its word, deleting a wrapped key that every
 * enrolled machine depends on. So absence of the hardware half is trusted
 * only when there is nothing else to lose.
 */
class KeyPlanTest {

    @Test
    fun aCompleteKeyIsKept() {
        assertEquals(KeyPlan.Keep, keyPlan(hardwareHalf = true, files = FilesState.COMPLETE))
    }

    @Test
    fun nothingAtAllMeansMakeOne() {
        assertEquals(KeyPlan.Create, keyPlan(hardwareHalf = false, files = FilesState.ABSENT))
    }

    /** A creation that stopped after the wrapped key: no public point was ever shown, so nothing depends on it. */
    @Test
    fun anUnfinishedCreationIsMadeAgain() {
        assertEquals(KeyPlan.Create, keyPlan(hardwareHalf = false, files = FilesState.INCOMPLETE))
        assertEquals(KeyPlan.Create, keyPlan(hardwareHalf = true, files = FilesState.INCOMPLETE))
    }

    /** The Keystore says yes and positively, and the files are not there: the old key of an older version, say. */
    @Test
    fun aHardwareKeyWithNoFilesHasNothingWrappedAndIsMadeAgain() {
        assertEquals(KeyPlan.Create, keyPlan(hardwareHalf = true, files = FilesState.ABSENT))
    }

    /** Could be a Keystore that did not answer; could be a lost key. Either way it is not for the app to delete. */
    @Test
    fun wrappedFilesWithNoHardwareKeyAreNotDeleted() {
        assertEquals(
            KeyPlan.Stop(KeyProblem.HARDWARE_HALF_MISSING),
            keyPlan(hardwareHalf = false, files = FilesState.COMPLETE),
        )
    }

    @Test
    fun damagedFilesAreNotDeletedEither() {
        assertEquals(
            KeyPlan.Stop(KeyProblem.FILES_DAMAGED),
            keyPlan(hardwareHalf = true, files = FilesState.DAMAGED),
        )
        assertEquals(
            KeyPlan.Stop(KeyProblem.FILES_DAMAGED),
            keyPlan(hardwareHalf = false, files = FilesState.DAMAGED),
        )
    }
}
