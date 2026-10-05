package com.mobileegress.agent.ui

import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class QrFrameDecoderTest {
    private val expected = generateSequence(File(requireNotNull(System.getProperty("user.dir")))) { it.parentFile }
        .map { File(it, "testdata/direct-v2-wire.json") }.first { it.isFile }.readText()
        .let { Json.parseToJsonElement(it).jsonObject.getValue("invitation").jsonPrimitive.content }
    private val fixture = requireNotNull(javaClass.getResourceAsStream("/qr/direct-v2-upright.txt")).bufferedReader().use { input ->
        val rows = input.readLines()
        require(rows.all { it.length == rows.size && it.all { bit -> bit == '0' || bit == '1' } })
        val width = rows.size * 4
        Frame(width, width, width, ByteArray(width * width) { at ->
            if (rows[at / width / 4][at % width / 4] == '1') 0 else 0xff.toByte()
        })
    }

    @Test fun `dense invitation missed upright still returns its exact payload`() {
        // This public Go-generated matrix characterizes the actual finder ambiguity.
        val originalReader = MultiFormatReader().apply {
            setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
        }
        assertThrows(NotFoundException::class.java) {
            originalReader.decodeWithState(BinaryBitmap(HybridBinarizer(PlanarYUVLuminanceSource(
                fixture.bytes, fixture.stride, fixture.height, 0, 0, fixture.width, fixture.height, false,
            ))))
        }
        assertEquals(expected, decode(QrFrameDecoder(), fixture))
    }

    @Test fun `all four orientations preserve the invitation bytes`() {
        val decoder = QrFrameDecoder()
        var frame = fixture
        repeat(4) {
            assertEquals(expected, decode(decoder, frame))
            frame = rotate(frame)
        }
    }

    @Test fun `ordinary detectable orientation still decodes without changing payload`() {
        assertEquals(expected, decode(QrFrameDecoder(), rotate(fixture)))
    }

    @Test fun `padded non-square camera rows and short final padding are supported`() {
        val width = fixture.width + 48
        val height = fixture.height + 24
        val stride = width + 17
        // Camera buffers need not retain padding after the last visible row.
        val bytes = ByteArray((height - 1) * stride + width) { 0xff.toByte() }
        repeat(fixture.height) { row ->
            fixture.bytes.copyInto(bytes, (row + 12) * stride + 24, row * fixture.stride, row * fixture.stride + fixture.width)
        }
        assertEquals(expected, decode(QrFrameDecoder(), Frame(width, height, stride, bytes)))
    }

    @Test fun `frames without a QR do not prevent a later successful decode`() {
        val decoder = QrFrameDecoder()
        for (shade in listOf(0, 255)) {
            assertThrows(NotFoundException::class.java) {
                decode(decoder, Frame(128, 96, 128, ByteArray(128 * 96) { shade.toByte() }))
            }
        }
        assertEquals(expected, decode(decoder, rotate(fixture)))
    }

    private fun decode(decoder: QrFrameDecoder, frame: Frame) =
        decoder.decode(frame.bytes, frame.width, frame.height, frame.stride)

    private fun rotate(frame: Frame): Frame {
        val bytes = ByteArray(frame.width * frame.height)
        for (y in 0 until frame.height) for (x in 0 until frame.width) {
            bytes[x * frame.height + frame.height - 1 - y] = frame.bytes[y * frame.stride + x]
        }
        return Frame(frame.height, frame.width, frame.height, bytes)
    }

    private data class Frame(val width: Int, val height: Int, val stride: Int, val bytes: ByteArray)
}
