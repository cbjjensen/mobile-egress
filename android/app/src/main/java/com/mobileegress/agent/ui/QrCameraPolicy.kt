package com.mobileegress.agent.ui

import zxingcpp.BarcodeReader

internal fun nativeQrOptions() = BarcodeReader.Options(
    formats = setOf(BarcodeReader.Format.QR_CODE),
    tryHarder = true,
    tryRotate = true,
    tryDownscale = true,
    tryInvert = false,
    tryDenoise = false,
    isPure = false,
    binarizer = BarcodeReader.Binarizer.LOCAL_AVERAGE,
    textMode = BarcodeReader.TextMode.PLAIN,
    maxNumberOfSymbols = 1,
    returnErrors = false,
)

internal fun qrAnalysisSizeAllowed(width: Int, height: Int) =
    width > 0 && height > 0 && maxOf(width, height) <= 1280 && minOf(width, height) <= 960
