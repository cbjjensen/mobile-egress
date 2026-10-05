package com.mobileegress.agent.ui

import android.util.Size
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.core.resolutionselector.AspectRatioStrategy
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.view.PreviewView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleOwner
import com.google.common.util.concurrent.ListenableFuture
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import zxingcpp.BarcodeReader

internal inline fun guardScannerInitialization(
    onScannerUnavailable: () -> Unit,
    block: () -> Unit,
) {
    try {
        block()
    } catch (_: Exception) {
        onScannerUnavailable()
    } catch (_: LinkageError) {
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
    val previewView = remember { PreviewView(context).apply { scaleType = PreviewView.ScaleType.FIT_CENTER } }
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
                        val decoder = BarcodeReader(nativeQrOptions())
                        val analysis = ImageAnalysis.Builder()
                            .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                            .setResolutionSelector(
                                ResolutionSelector.Builder()
                                    .setAspectRatioStrategy(AspectRatioStrategy.RATIO_4_3_FALLBACK_AUTO_STRATEGY)
                                    .setResolutionStrategy(ResolutionStrategy(Size(1280, 960), ResolutionStrategy.FALLBACK_RULE_CLOSEST_LOWER))
                                    .setResolutionFilter { sizes, _ -> sizes.filter { qrAnalysisSizeAllowed(it.width, it.height) } }
                                    .build(),
                            )
                            .build()
                            .also { imageAnalysis ->
                                imageAnalysis.setAnalyzer(analysisExecutor) { imageProxy ->
                                    analyzeQrFrame(
                                        imageProxy,
                                        read = { frame -> decoder.read(frame).firstOrNull()?.let { it.text ?: "" } },
                                        onDecoded = { value -> dispatchAcceptedResult { latestOnQrDecoded.value(value) } },
                                        onUnrecognized = { dispatchAcceptedResult { latestOnQrNotRecognized.value() } },
                                        onUnavailable = { dispatchAcceptedResult { latestOnScannerUnavailable.value() } },
                                    )
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
