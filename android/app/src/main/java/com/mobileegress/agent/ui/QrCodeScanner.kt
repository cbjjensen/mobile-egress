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
import com.google.common.util.concurrent.ListenableFuture
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

internal inline fun guardScannerInitialization(
    onScannerUnavailable: () -> Unit,
    block: () -> Unit,
) {
    try {
        block()
    } catch (_: Exception) {
        onScannerUnavailable()
    }
}

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
    val mainExecutor = remember(context) { ContextCompat.getMainExecutor(context) }

    DisposableEffect(lifecycleOwner) {
        val accepted = AtomicBoolean(false)
        val disposed = AtomicBoolean(false)
        var cameraProviderFuture: ListenableFuture<ProcessCameraProvider>? = null
        val dispatchAcceptedResult = { callback: () -> Unit ->
            if (accepted.compareAndSet(false, true)) {
                mainExecutor.execute {
                    if (!disposed.get()) callback()
                }
            }
        }
        val reportScannerUnavailable = {
            if (!disposed.get() && accepted.compareAndSet(false, true)) {
                latestOnScannerUnavailable.value()
            }
        }
        guardScannerInitialization(reportScannerUnavailable) {
            val providerFuture = ProcessCameraProvider.getInstance(context)
            cameraProviderFuture = providerFuture
            providerFuture.addListener(
                {
                    if (disposed.get()) return@addListener
                    var boundProvider: ProcessCameraProvider? = null
                    guardScannerInitialization(
                        onScannerUnavailable = {
                            runCatching { boundProvider?.unbindAll() }
                            reportScannerUnavailable()
                        },
                    ) {
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
                                        dispatchAcceptedResult { latestOnQrDecoded.value(result.text) }
                                    } catch (_: NotFoundException) {
                                        // Keep scanning until a QR code is visible.
                                    } catch (_: FormatException) {
                                        dispatchAcceptedResult { latestOnQrNotRecognized.value() }
                                    } catch (_: ChecksumException) {
                                        dispatchAcceptedResult { latestOnQrNotRecognized.value() }
                                    } finally {
                                        reader.reset()
                                        imageProxy.close()
                                    }
                                }
                            }
                        val provider = providerFuture.get()
                        boundProvider = provider
                        if (!disposed.get()) {
                            provider.unbindAll()
                            provider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
                        }
                    }
                },
                ContextCompat.getMainExecutor(context),
            )
        }

        onDispose {
            disposed.set(true)
            if (cameraProviderFuture?.isDone == true) {
                runCatching { cameraProviderFuture?.get()?.unbindAll() }
            }
            analysisExecutor.shutdown()
        }
    }

    AndroidView(factory = { previewView }, modifier = modifier)
}
