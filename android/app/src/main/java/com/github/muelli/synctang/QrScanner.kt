// SPDX-License-Identifier: AGPL-3.0-or-later
package com.github.muelli.synctang

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
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
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
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
    onScanned: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    var granted by remember { mutableStateOf(context.hasCameraPermission()) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        granted = it
    }

    if (!granted) {
        Button(onClick = { ask.launch(Manifest.permission.CAMERA) }, modifier = modifier.fillMaxWidth()) {
            Text(stringResource(R.string.pair_grant_camera))
        }
        return
    }

    val lifecycleOwner = LocalLifecycleOwner.current
    val executor = remember { Executors.newSingleThreadExecutor() }
    // One decode is enough: the pairing screen closes on the first
    // result, and delivering the same code repeatedly would fight the
    // navigation that follows.
    var delivered by remember { mutableStateOf(false) }

    DisposableEffect(Unit) {
        onDispose { executor.shutdown() }
    }

    Box(modifier) {
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
                        if (text != null && !delivered) {
                            delivered = true
                            previewView.post { onScanned(text) }
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
