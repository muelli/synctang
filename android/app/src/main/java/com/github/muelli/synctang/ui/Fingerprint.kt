// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang.ui

private const val ELLIPSIS = "…"
private val deviceIdShape = Regex("[A-Za-z2-7]{7}(-[A-Za-z2-7]{7}){7}")
private val hex = Regex("[0-9A-Fa-f]+")

/** `AAAAAAA-BBBBBBB-…-HHHHHHH` for a device ID; anything else unchanged. */
fun shortDeviceId(id: String): String {
    if (!deviceIdShape.matches(id)) return id
    val groups = id.split('-')
    return "${groups[0]}-${groups[1]}-$ELLIPSIS-${groups.last()}"
}

/** The first sixteen hex digits in groups of four; anything not hex unchanged. */
fun shortKeyId(hexId: String): String {
    if (!hex.matches(hexId)) return hexId
    val grouped = hexId.take(16).chunked(4).joinToString(" ")
    return if (hexId.length > 16) "$grouped $ELLIPSIS" else grouped
}

/** Whether two device IDs are the same, ignoring case, dashes, and spaces. */
fun sameDeviceId(a: String, b: String): Boolean {
    fun normal(id: String) = id.filterNot { it == '-' || it.isWhitespace() }.uppercase()
    val left = normal(a)
    return left.isNotEmpty() && left == normal(b)
}
