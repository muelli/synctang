// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.io.File

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
    fun exists(): Boolean = wrappedFile.isFile && publicFile.isFile

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
        temporary.writeBytes(bytes)
        check(temporary.renameTo(target)) { "could not move ${temporary.name} into place" }
    }

    private fun isUncompressedP256Point(bytes: ByteArray) = bytes.size == 65 && bytes[0] == 4.toByte()
}
