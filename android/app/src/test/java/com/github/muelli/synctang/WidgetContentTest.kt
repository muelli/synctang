// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import org.junit.Assert.assertEquals
import org.junit.Test

class WidgetContentTest {
    private val a = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "study")
    private val b = Machine("BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB-BBBBBBB", "attic")

    @Test
    fun withNoMachineTheWidgetSaysSo() {
        assertEquals(WidgetContent.NotPaired, widgetContent(emptyList()))
    }

    @Test
    fun theWidgetUnlocksTheMachineUsedLast() {
        assertEquals(WidgetContent.ForMachine(b.deviceId, "attic"), widgetContent(listOf(b, a)))
    }

    @Test
    fun theNameOnTheWidgetIsTheCleanedAndShortenedOne() {
        val hostile = a.copy(name = "st\u001b[2Judy" + "y".repeat(100))
        val shown = (widgetContent(listOf(hostile)) as WidgetContent.ForMachine).label
        assertEquals(launcherLabel(hostile), shown)
        assertEquals(SHORTCUT_LABEL_MAX, shown.length)
    }
}
