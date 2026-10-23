// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.ReaderException
import com.google.zxing.common.HybridBinarizer
import java.util.concurrent.Executors

/**
 * A QR scanner built from CameraX and ZXing's pure-Java core.
 *
 * Deliberately not ML Kit or the Play Services barcode scanner: both
 * are proprietary, and F-Droid will not build an app that depends on
 * them. ZXing decodes the camera's luminance plane directly, which
 * needs no conversion and no native code.
 */
@Composable
fun QrScanner(
    onScanned: (String) -> Boolean,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    var access by remember { mutableStateOf(initialCameraAccess(context.hasCameraPermission())) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        val rationale = context.findActivity()?.let {
            ActivityCompat.shouldShowRequestPermissionRationale(it, Manifest.permission.CAMERA)
        } ?: true
        access = cameraAccessAfterRequest(ok, rationale)
    }

    // Someone sent to Settings comes back through onResume, having
    // perhaps allowed the camera there.
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME && context.hasCameraPermission()) {
                access = CameraAccess.GRANTED
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    when (access) {
        CameraAccess.GRANTED -> Unit
        CameraAccess.ASK -> {
            Column(modifier) {
                Button(
                    onClick = { ask.launch(Manifest.permission.CAMERA) },
                    modifier = Modifier.fillMaxWidth().focusRing(),
                ) {
                    Text(stringResource(R.string.pair_grant_camera))
                }
            }
            return
        }
        CameraAccess.OPEN_SETTINGS -> {
            Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.pair_camera_denied), style = MaterialTheme.typography.bodyMedium)
                Button(
                    onClick = { context.startActivity(appSettingsIntent(context)) },
                    modifier = Modifier.fillMaxWidth().focusRing(),
                ) {
                    Text(stringResource(R.string.pair_open_settings))
                }
            }
            return
        }
    }

    val executor = remember { Executors.newSingleThreadExecutor() }
    // An accepted code ends scanning, since the pairing screen closes
    // on it. A rejected one re-arms the latch so the camera keeps
    // looking, but the same text is not re-offered for a couple of
    // seconds, or one bad code would fire the callback every frame.
    val latch = remember { ScanLatch { android.os.SystemClock.elapsedRealtime() } }

    DisposableEffect(Unit) {
        onDispose { executor.shutdown() }
    }

    // The preview is no use to a keyboard, so Tab skips it; the buttons
    // shown instead of it above are reachable as usual.
    Box(modifier.notKeyboardFocusable()) {
        AndroidView(
            modifier = Modifier.fillMaxWidth(),
            factory = { viewContext ->
                val previewView = PreviewView(viewContext)
                val providerFuture = ProcessCameraProvider.getInstance(viewContext)
                providerFuture.addListener({
                    val provider = providerFuture.get()

                    val preview = Preview.Builder().build().also {
                        it.setSurfaceProvider(previewView.surfaceProvider)
                    }
                    val analysis = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()
                    analysis.setAnalyzer(executor) { image ->
                        val text = image.decodeQrCode()
                        image.close()
                        if (text != null && latch.offer(text)) {
                            previewView.post {
                                if (onScanned(text)) latch.accepted() else latch.rejected()
                            }
                        }
                    }

                    provider.unbindAll()
                    provider.bindToLifecycle(
                        lifecycleOwner,
                        CameraSelector.DEFAULT_BACK_CAMERA,
                        preview,
                        analysis,
                    )
                }, ContextCompat.getMainExecutor(viewContext))
                previewView
            },
        )
    }
}

private const val REPEAT_IGNORE_MS = 2000L

/**
 * Decides which decoded codes reach the caller. Rejected scans re-arm,
 * so a bad code does not leave the camera dead; identical text is
 * debounced for [REPEAT_IGNORE_MS] since the analyser sees the same
 * code on every frame. Touched from the analyser thread and the main
 * thread, hence synchronized.
 */
class ScanLatch(private val now: () -> Long) {
    private var done = false
    private var inFlight = false
    private var lastText: String? = null
    private var lastOffered = 0L

    @Synchronized
    fun offer(text: String): Boolean {
        if (done || inFlight) return false
        val t = now()
        if (text == lastText && t - lastOffered < REPEAT_IGNORE_MS) return false
        inFlight = true
        lastText = text
        lastOffered = t
        return true
    }

    @Synchronized
    fun accepted() {
        done = true
        inFlight = false
    }

    @Synchronized
    fun rejected() {
        inFlight = false
    }
}

/** Whether the scanner can show the camera, or what it must offer instead. */
enum class CameraAccess { GRANTED, ASK, OPEN_SETTINGS }

fun initialCameraAccess(hasPermission: Boolean): CameraAccess =
    if (hasPermission) CameraAccess.GRANTED else CameraAccess.ASK

/**
 * A refusal Android would still show a rationale for can be asked
 * again. One with no rationale left means the dialog will not appear
 * again, so asking would do nothing; only the app's page in Settings
 * can grant it now.
 */
fun cameraAccessAfterRequest(granted: Boolean, showRationale: Boolean): CameraAccess = when {
    granted -> CameraAccess.GRANTED
    showRationale -> CameraAccess.ASK
    else -> CameraAccess.OPEN_SETTINGS
}

private fun appSettingsIntent(context: Context): Intent =
    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null))

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

private fun Context.hasCameraPermission(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) ==
        PackageManager.PERMISSION_GRANTED

private val reader = MultiFormatReader().apply {
    setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE)))
}

/**
 * Decodes the frame's luminance plane. CameraX delivers YUV_420_888,
 * whose first plane is exactly the greyscale image ZXing wants, so
 * there is no colour conversion to get wrong or to pay for.
 */
private fun ImageProxy.decodeQrCode(): String? {
    val plane = planes.firstOrNull() ?: return null
    val bytes = ByteArray(plane.buffer.remaining())
    plane.buffer.get(bytes)

    val source = PlanarYUVLuminanceSource(
        bytes,
        plane.rowStride,
        height,
        0,
        0,
        width,
        height,
        false,
    )
    return try {
        reader.decodeWithState(BinaryBitmap(HybridBinarizer(source))).text
    } catch (e: ReaderException) {
        // No code in this frame, which is the common case.
        null
    } finally {
        reader.reset()
    }
}
