// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.fake

/**
 * Stands in for android.security.KeyStoreException, which cannot be built outside
 * a device. Public, because the code under test reads it by reflection, as it does
 * the real one.
 */
class KeyStoreException(private val code: Int) : Exception("whatever the platform says") {
    fun getErrorCode() = code
}
