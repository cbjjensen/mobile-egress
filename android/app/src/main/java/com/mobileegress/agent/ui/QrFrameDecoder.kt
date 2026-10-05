package com.mobileegress.agent.ui

import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer

/** Owned by the scanner's single analysis executor; never retains a camera frame. */
internal class QrFrameDecoder {
    private val reader = MultiFormatReader().apply {
        setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
    }

    fun decode(bytes: ByteArray, width: Int, height: Int, rowStride: Int): String = try {
        val source = PlanarYUVLuminanceSource(bytes, rowStride, height, 0, 0, width, height, false)
        try {
            reader.decodeWithState(BinaryBitmap(HybridBinarizer(source))).text
        } catch (_: NotFoundException) {
            // Dense symbols can confuse ZXing's finder in only one orientation.
            // Retry once, on the same retained frame; padding is not image data.
            reader.reset()
            val rotated = ByteArray(width * height)
            for (y in 0 until height) for (x in 0 until width) {
                rotated[x * height + height - 1 - y] = bytes[y * rowStride + x]
            }
            val turned = PlanarYUVLuminanceSource(rotated, height, width, 0, 0, height, width, false)
            reader.decodeWithState(BinaryBitmap(HybridBinarizer(turned))).text
        }
    } finally {
        reader.reset()
    }
}
