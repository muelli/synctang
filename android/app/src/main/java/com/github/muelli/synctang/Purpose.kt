// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

/**
 * What a machine said in its greeting it will do with the answer, when that is not an ordinary unlock, as a verb
 * phrase: "enrol a key holder". The three purposes the app knows have strings of their own; any other is passed
 * on as the machine put it (the library has cleaned and cut it), because a purpose from a newer machine is
 * still something other than an unlock. It is the machine's own word and an honest machine's advice, never proof:
 * a machine that does something else says nothing, which looks like an unlock.
 */
fun purposeLabel(purpose: String): Label = when (purpose) {
    "enrol" -> Label(R.string.purpose_enrol)
    "pair" -> Label(R.string.purpose_pair)
    "extract" -> Label(R.string.purpose_extract)
    else -> Label(R.string.purpose_other, listOf(purpose))
}
