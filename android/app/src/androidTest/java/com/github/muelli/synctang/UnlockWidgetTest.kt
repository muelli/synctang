// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.appwidget.AppWidgetProviderInfo
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.graphics.Bitmap
import android.graphics.Canvas
import android.util.SizeF
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.TextView
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Test

/**
 * The widget as installed: what the system is told about it, that each layout it offers inflates and says
 * what it should, and that a tap on it does what the shortcut does. Pictures of it at several sizes are
 * written to the app's files directory for a person to look at (`widget-<w>x<h>.png`).
 */
class UnlockWidgetTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val machine = Machine("AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA-AAAAAAA", "attic")
    private val before = PairingStore(context).machine()

    @Before
    fun pair() {
        PairingStore(context).saveMachine(machine)
    }

    @After
    fun unpair() {
        if (before == null) PairingStore(context).startNewIdentity() else PairingStore(context).saveMachine(before)
    }

    private fun inflate(layout: android.widget.RemoteViews): View = layout.apply(context, FrameLayout(context))

    private fun dp(value: Float) = (value * context.resources.displayMetrics.density).toInt()

    @Test
    fun theSystemKnowsAResizableWidgetWithADescriptionAndAPreview() {
        val info = AppWidgetManager.getInstance(context).installedProviders.firstOrNull {
            it.provider == ComponentName(context, UnlockWidgetProvider::class.java)
        }
        assumeTrue("the widget is not listed (a device without widget support)", info != null)
        info!!
        assertTrue(info.resizeMode and AppWidgetProviderInfo.RESIZE_HORIZONTAL != 0)
        assertTrue(info.resizeMode and AppWidgetProviderInfo.RESIZE_VERTICAL != 0)
        assertEquals(2, info.targetCellWidth)
        assertEquals(1, info.targetCellHeight)
        assertTrue(info.minResizeWidth in 1..dp(60f).coerceAtLeast(120))
        assertNotEquals(0, info.previewLayout)
        assertEquals(0, info.updatePeriodMillis)
    }

    @Test
    fun everyLayoutInflatesAndNamesTheMachine() {
        val layouts = UnlockWidgetProvider.layouts(context, widgetContent(listOf(machine)), 1)
        assertEquals(3, layouts.size)
        for ((size, remote) in layouts) {
            val view = inflate(remote)
            val background = view.findViewById<View>(android.R.id.background)
            assertEquals("Unlock attic", background.contentDescription.toString())
            val names = view.findViewById<TextView?>(R.id.widget_name)
            if (size.width > 100f) assertEquals("attic", names!!.text.toString()) else assertEquals(null, names)
        }
    }

    @Test
    fun withNoMachineTheWidgetSaysSoAndOpensTheApp() {
        val layouts = UnlockWidgetProvider.layouts(context, WidgetContent.NotPaired, 1)
        val wide = inflate(layouts.getValue(SizeF(110f, 40f)))
        assertEquals(context.getString(R.string.widget_unpaired_name), wide.findViewById<TextView>(R.id.widget_name).text.toString())
        assertEquals(context.getString(R.string.widget_unpaired_for), wide.findViewById<View>(android.R.id.background).contentDescription.toString())
    }

    @Test
    fun aTapOnTheWidgetStartsTheUnlockOfThatMachine() {
        val connecting = context.getString(R.string.unlock_connecting)
        val ready = context.getString(R.string.ready_title)
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            var title = ""
            fun wait(text: String) {
                val end = System.currentTimeMillis() + 10_000
                while (System.currentTimeMillis() < end) {
                    scenario.onActivity { title = it.title.toString() }
                    if (title.contains(text)) return
                    Thread.sleep(100)
                }
            }
            wait(ready)
            assumeTrue("not on the Unlock screen: $title", title.contains(ready))

            val wide = inflate(UnlockWidgetProvider.layouts(context, widgetContent(listOf(machine)), 1).getValue(SizeF(110f, 40f)))
            instrumentation.runOnMainSync { wide.findViewById<View>(android.R.id.background).performClick() }
            wait(connecting)
            assertTrue(title, title.contains(connecting))
        }
    }

    @Test
    fun theLayoutsAreDrawnAtSeveralSizesForALook() {
        val directory = File(context.getExternalFilesDir(null), "widgets").apply { mkdirs() }
        val layouts = UnlockWidgetProvider.layouts(context, widgetContent(listOf(machine.copy(name = "attic with a rather long name indeed"))), 1)
        for ((w, h) in listOf(40 to 40, 72 to 72, 110 to 40, 180 to 40, 250 to 110, 110 to 110, 180 to 250)) {
            // What the system does: the layout that is for the largest size that still fits.
            val remote = layouts.entries.filter { it.key.width <= w && it.key.height <= h }
                .maxByOrNull { it.key.width * it.key.height }?.value ?: layouts.getValue(SizeF(40f, 40f))
            val view = inflate(remote)
            view.measure(
                View.MeasureSpec.makeMeasureSpec(dp(w.toFloat()), View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(dp(h.toFloat()), View.MeasureSpec.EXACTLY),
            )
            view.layout(0, 0, dp(w.toFloat()), dp(h.toFloat()))
            val bitmap = Bitmap.createBitmap(dp(w.toFloat()), dp(h.toFloat()), Bitmap.Config.ARGB_8888)
            view.draw(Canvas(bitmap))
            File(directory, "widget-${w}x$h.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        assertTrue(directory.listFiles()!!.isNotEmpty())
    }
}
