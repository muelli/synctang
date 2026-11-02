// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.io.File
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * Where the wrapped key and the public point are kept. The wrapped key is
 * useless without the hardware key that wraps it, and the public point is public,
 * so the files need no secrecy of their own; what matters is that a half-written
 * or half-deleted pair is never mistaken for a key.
 */
class WrappedKeyStoreTest {

    @get:Rule val folder = TemporaryFolder()

    private val wrapped = ByteArray(256) { it.toByte() }
    private val public = byteArrayOf(4) + ByteArray(64) { (it + 1).toByte() }

    private fun store() = WrappedKeyStore(folder.root, "alias")

    @Test
    fun whatWasSavedIsLoadedBack() {
        store().save(wrapped, public)
        assertTrue(store().exists())
        assertArrayEquals(wrapped, store().loadWrapped())
        assertArrayEquals(public, store().loadPublic())
    }

    @Test
    fun theStateOfAnEmptyDirectoryIsAbsent() {
        assertEquals(FilesState.ABSENT, store().state())
    }

    @Test
    fun theStateOfASavedPairIsComplete() {
        store().save(wrapped, public)
        assertEquals(FilesState.COMPLETE, store().state())
    }

    /** What a crash between the two writes leaves: no public point, so nothing was ever enrolled on it. */
    @Test
    fun aWrappedKeyWithNoPublicPointIsIncomplete() {
        File(folder.root, "alias.wrapped").writeBytes(wrapped)
        assertEquals(FilesState.INCOMPLETE, store().state())
    }

    @Test
    fun aPublicPointWithNoWrappedKeyIsDamaged() {
        File(folder.root, "alias.public").writeBytes(public)
        assertEquals(FilesState.DAMAGED, store().state())
    }

    /** What a power cut after a rename but before the data reached the disk looks like. */
    @Test
    fun emptyFilesAreDamaged() {
        store().save(wrapped, public)
        File(folder.root, "alias.wrapped").writeBytes(ByteArray(0))
        assertEquals(FilesState.DAMAGED, store().state())

        store().save(wrapped, public)
        File(folder.root, "alias.public").writeBytes(ByteArray(0))
        assertEquals(FilesState.DAMAGED, store().state())
    }

    @Test
    fun aPublicPointOfTheWrongShapeIsDamaged() {
        store().save(wrapped, public)
        File(folder.root, "alias.public").writeBytes(ByteArray(65) { 7 })
        assertEquals(FilesState.DAMAGED, store().state())
    }

    @Test
    fun leftoverTemporaryFilesAreNotAKey() {
        File(folder.root, "alias.wrapped.tmp").writeBytes(wrapped)
        File(folder.root, "alias.public.tmp").writeBytes(public)
        assertEquals(FilesState.ABSENT, store().state())
    }

    @Test
    fun nothingExistsBeforeASave() {
        assertFalse(store().exists())
    }

    @Test
    fun aPairWithOneFileMissingIsNotAKey() {
        store().save(wrapped, public)
        File(folder.root, "alias.public").delete()
        assertFalse(store().exists())
    }

    @Test
    fun deletingRemovesBothFiles() {
        store().save(wrapped, public)
        store().delete()
        assertFalse(store().exists())
        assertEquals(emptyList<String>(), folder.root.list()!!.toList())
    }

    @Test
    fun aSaveLeavesNoTemporaryFileBehind() {
        store().save(wrapped, public)
        assertEquals(setOf("alias.wrapped", "alias.public"), folder.root.list()!!.toSet())
    }

    @Test
    fun aSecondSaveReplacesTheFirst() {
        store().save(wrapped, public)
        val other = ByteArray(256) { 7 }
        store().save(other, public)
        assertArrayEquals(other, store().loadWrapped())
    }

    @Test
    fun twoAliasesDoNotShareFiles() {
        store().save(wrapped, public)
        assertFalse(WrappedKeyStore(folder.root, "another").exists())
    }

    // A public point that is not a SEC1 uncompressed P-256 point would
    // be published to machines as this phone's key.
    @Test
    fun aPublicPointOfTheWrongShapeIsRefusedOnSaveAndOnLoad() {
        for (bad in listOf(ByteArray(64), ByteArray(66), byteArrayOf(2) + ByteArray(64))) {
            try {
                store().save(wrapped, bad)
                fail("saved a public point of ${bad.size} bytes starting ${bad.firstOrNull()}")
            } catch (e: IllegalArgumentException) {
                // expected
            }
        }
        store().save(wrapped, public)
        File(folder.root, "alias.public").writeBytes(ByteArray(10))
        try {
            store().loadPublic()
            fail("loaded a corrupt public point")
        } catch (e: IllegalStateException) {
            // expected
        }
    }

    @Test
    fun anEmptyWrappedKeyIsRefused() {
        try {
            store().save(ByteArray(0), public)
            fail("saved an empty wrapped key")
        } catch (e: IllegalArgumentException) {
            // expected
        }
    }
}
