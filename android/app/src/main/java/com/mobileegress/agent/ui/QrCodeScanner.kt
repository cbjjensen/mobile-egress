package com.mobileegress.agent.ui

import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleOwner
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.ChecksumException
import com.google.zxing.DecodeHintType
import com.google.zxing.FormatException
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

@Composable
fun QrCodeScanner(
    lifecycleOwner: LifecycleOwner,
    modifier: Modifier = Modifier,
    onQrDecoded: (String) -> Unit,
    onQrNotRecognized: () -> Unit,
    onScannerUnavailable: () -> Unit,
) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val previewView = remember { PreviewView(context) }
    val latestOnQrDecoded = rememberUpdatedState(onQrDecoded)
    val latestOnQrNotRecognized = rememberUpdatedState(onQrNotRecognized)
    val latestOnScannerUnavailable = rememberUpdatedState(onScannerUnavailable)
    val analysisExecutor = remember { Executors.newSingleThreadExecutor() }

    DisposableEffect(lifecycleOwner) {
        val cameraProviderFuture = ProcessCameraProvider.getInstance(context)
        val accepted = AtomicBoolean(false)
        val disposed = AtomicBoolean(false)
        cameraProviderFuture.addListener(
            {
                if (disposed.get()) return@addListener
                try {
                    val preview = Preview.Builder().build().also { preview ->
                        preview.surfaceProvider = previewView.surfaceProvider
                    }
                    val reader = MultiFormatReader().apply {
                        setHints(
                            mapOf(
                                DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE),
                                DecodeHintType.TRY_HARDER to true,
                            ),
                        )
                    }
                    val analysis = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()
                        .also { imageAnalysis ->
                            imageAnalysis.setAnalyzer(analysisExecutor) { imageProxy ->
                                try {
                                    val luminance = imageProxy.planes.firstOrNull()
                                        ?: return@setAnalyzer
                                    val buffer = luminance.buffer
                                    val bytes = ByteArray(buffer.remaining())
                                    buffer.get(bytes)
                                    val source = PlanarYUVLuminanceSource(
                                        bytes,
                                        luminance.rowStride,
                                        imageProxy.height,
                                        0,
                                        0,
                                        imageProxy.width,
                                        imageProxy.height,
                                        false,
                                    )
                                    val result = reader.decodeWithState(BinaryBitmap(HybridBinarizer(source)))
                                    if (accepted.compareAndSet(false, true)) {
                                        latestOnQrDecoded.value(result.text)
                                    }
                                } catch (_: NotFoundException) {
                                    // Keep scanning until a QR code is visible.
                                } catch (_: FormatException) {
                                    if (accepted.compareAndSet(false, true)) latestOnQrNotRecognized.value()
                                } catch (_: ChecksumException) {
                                    if (accepted.compareAndSet(false, true)) latestOnQrNotRecognized.value()
                                } finally {
                                    reader.reset()
                                    imageProxy.close()
                                }
                            }
                        }
                    val provider = cameraProviderFuture.get()
                    if (disposed.get()) return@addListener
                    provider.unbindAll()
                    provider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
                } catch (_: Exception) {
                    if (cameraProviderFuture.isDone) {
                        runCatching { cameraProviderFuture.get().unbindAll() }
                    }
                    if (accepted.compareAndSet(false, true)) latestOnScannerUnavailable.value()
                }
            },
            ContextCompat.getMainExecutor(context),
        )

        onDispose {
            disposed.set(true)
            if (cameraProviderFuture.isDone) runCatching { cameraProviderFuture.get().unbindAll() }
            analysisExecutor.shutdown()
        }
    }

    AndroidView(factory = { previewView }, modifier = modifier)
}
