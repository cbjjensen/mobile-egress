package com.mobileegress.agent.ui

import org.junit.Assert.*
import org.junit.Test
import zxingcpp.BarcodeReader

class QrCameraPolicyTest {
    @Test fun `native options limit output and match the tested offline QR detector`() {
        val options = nativeQrOptions()
        assertEquals(setOf(BarcodeReader.Format.QR_CODE), options.formats)
        assertEquals(1, options.maxNumberOfSymbols)
        assertTrue(options.tryHarder)
        assertTrue(options.tryRotate)
        assertTrue(options.tryDownscale)
        assertFalse(options.tryInvert)
        assertFalse(options.tryDenoise)
        assertFalse(options.isPure)
        assertFalse(options.returnErrors)
        assertEquals(BarcodeReader.Binarizer.LOCAL_AVERAGE, options.binarizer)
        assertEquals(BarcodeReader.TextMode.PLAIN, options.textMode)
    }

    @Test fun `supported analysis sizes stay within the same bound in either orientation`() {
        for ((width, height) in listOf(1280 to 960, 960 to 1280, 1280 to 720, 640 to 480)) {
            assertTrue(qrAnalysisSizeAllowed(width, height))
        }
        for ((width, height) in listOf(1920 to 1080, 1440 to 720, 1280 to 1024, 0 to 480, 640 to -1, Int.MAX_VALUE to Int.MAX_VALUE)) {
            assertFalse(qrAnalysisSizeAllowed(width, height))
        }
    }
}
