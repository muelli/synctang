// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import java.math.BigInteger
import java.security.AlgorithmParameters
import java.security.KeyFactory
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec

private const val COORD = 32

private val p256: ECParameterSpec by lazy {
    AlgorithmParameters.getInstance("EC").apply { init(ECGenParameterSpec("secp256r1")) }.getParameterSpec(ECParameterSpec::class.java)
}

/** A P-256 public key as the machine writes a point: SEC1 uncompressed, 0x04 then x then y, 32 bytes each. */
fun sec1Of(key: ECPublicKey): ByteArray {
    val out = ByteArray(1 + 2 * COORD)
    out[0] = 4
    unsigned(key.w.affineX).copyInto(out, 1)
    unsigned(key.w.affineY).copyInto(out, 1 + COORD)
    return out
}

/**
 * A P-256 public key from a point in that form. Anything that is not 65 bytes starting with 0x04 is refused; whether the point
 * is on the curve is checked by whoever hands it over (the Go side validates the machine's challenge before the
 * Keystore sees it), and by the Keystore itself.
 */
fun ecPublicKeyFromSec1(point: ByteArray): ECPublicKey {
    require(point.size == 1 + 2 * COORD && point[0].toInt() == 4) { "not an uncompressed P-256 point: ${point.size} bytes" }
    val x = BigInteger(1, point.copyOfRange(1, 1 + COORD))
    val y = BigInteger(1, point.copyOfRange(1 + COORD, 1 + 2 * COORD))
    return KeyFactory.getInstance("EC").generatePublic(ECPublicKeySpec(ECPoint(x, y), p256)) as ECPublicKey
}

/** [n] as exactly 32 big-endian bytes: BigInteger drops leading zeros and may add a sign byte. */
private fun unsigned(n: BigInteger): ByteArray {
    val raw = n.toByteArray()
    val trimmed = if (raw.size > COORD && raw[0].toInt() == 0) raw.copyOfRange(1, raw.size) else raw
    require(trimmed.size <= COORD) { "a coordinate of ${trimmed.size} bytes" }
    return ByteArray(COORD).also { trimmed.copyInto(it, COORD - trimmed.size) }
}
