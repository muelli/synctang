// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.io.File
import java.io.FileOutputStream

/**
 * Where the long-term key is kept: the private scalar wrapped by a key in the
 * phone's secure hardware, and the public point it belongs to.
 *
 * The wrapped scalar is useless without that hardware key, so the file needs no
 * secrecy of its own; it belongs in a directory Android does not back up, so a
 * restore onto another phone cannot produce a file nothing can unwrap. The public
 * point is public. What matters here is the pair: both files exist or there is no
 * key, and a half-written or half-deleted pair is never mistaken for one.
 */
class WrappedKeyStore(private val directory: File, private val name: String) {

    private val wrappedFile get() = File(directory, "$name.wrapped")
    private val publicFile get() = File(directory, "$name.public")

    /** Whether a complete pair is there. */
    fun exists(): Boolean = state() == FilesState.COMPLETE

    /**
     * What is on disk. Only the wrapped key without the public point is
     * [FilesState.INCOMPLETE]: that is what a crash between the two writes leaves,
     * and the public point, the only thing a machine is ever enrolled against, was
     * never shown. The public point alone, or either file empty or the wrong shape,
     * is [FilesState.DAMAGED]: it may have been enrolled, so it is not for the app
     * to clear.
     */
    fun state(): FilesState {
        val hasWrapped = wrappedFile.isFile
        val hasPublic = publicFile.isFile
        return when {
            !hasWrapped && !hasPublic -> FilesState.ABSENT
            hasWrapped && !hasPublic -> if (wrappedFile.length() > 0) FilesState.INCOMPLETE else FilesState.DAMAGED
            !hasWrapped -> FilesState.DAMAGED
            wrappedFile.length() == 0L || !isUncompressedP256Point(publicFile.readBytes()) -> FilesState.DAMAGED
            else -> FilesState.COMPLETE
        }
    }

    /**
     * Writes both files, each through a temporary file and a rename, the wrapped
     * key first: a crash between the two leaves no public file and so no key.
     */
    fun save(wrapped: ByteArray, publicPoint: ByteArray) {
        require(wrapped.isNotEmpty()) { "a wrapped key cannot be empty" }
        require(isUncompressedP256Point(publicPoint)) { "the public point is not an uncompressed P-256 point" }
        directory.mkdirs()
        replace(wrappedFile, wrapped)
        replace(publicFile, publicPoint)
    }

    fun loadWrapped(): ByteArray = wrappedFile.readBytes()

    /** The public point, as it was saved. Throws if the file has been damaged. */
    fun loadPublic(): ByteArray {
        val bytes = publicFile.readBytes()
        check(isUncompressedP256Point(bytes)) { "the stored public point is damaged (${bytes.size} bytes)" }
        return bytes
    }

    fun delete() {
        wrappedFile.delete()
        publicFile.delete()
        File(directory, "${wrappedFile.name}.tmp").delete()
        File(directory, "${publicFile.name}.tmp").delete()
    }

    private fun replace(target: File, bytes: ByteArray) {
        val temporary = File(directory, "${target.name}.tmp")
        // Written through to the disk before the rename: a rename that survives a
        // power cut while the data does not leaves an empty file under the real name.
        FileOutputStream(temporary).use {
            it.write(bytes)
            it.fd.sync()
        }
        check(temporary.renameTo(target)) { "could not move ${temporary.name} into place" }
    }

    private fun isUncompressedP256Point(bytes: ByteArray) = bytes.size == 65 && bytes[0] == 4.toByte()
}

/** What [WrappedKeyStore] finds on disk. */
enum class FilesState { ABSENT, INCOMPLETE, COMPLETE, DAMAGED }
