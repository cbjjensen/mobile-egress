package com.mobileegress.agent.pairing

import com.mobileegress.agent.direct.DirectBundles
import com.mobileegress.agent.direct.DirectException
import com.mobileegress.agent.direct.DirectRecord
import java.io.ByteArrayOutputStream
import java.io.File
import java.time.Instant
import java.util.Base64
import java.util.zip.DeflaterOutputStream
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class CompactQrEnvelopeTest {
    private val fixture = fixture("compact-qr-v1.json")
    private val valid = fixture.getValue("valid").jsonArray.map { it.jsonObject }

    @Test fun `plain setup codes and surrounding paste whitespace remain compatible`() {
        valid.forEach { item ->
            val original = item.value("original")
            assertEquals(original, CompactQrEnvelope.normalizeUserInput(original))
            assertEquals(original, CompactQrEnvelope.normalizeUserInput(" \r\n\t$original\n "))
        }
    }

    @Test fun `shared compact invitations and signed updates restore exact original bytes`() {
        valid.forEach { item ->
            val actual = CompactQrEnvelope.normalizeUserInput("\n ${item.value("compact")} \r\n")
            assertEquals(item.value("name"), item.value("original"), actual)
            assertEquals(DirectBundles.type(item.value("original")), DirectBundles.type(actual))
        }
    }

    @Test fun `malformed shared envelopes are rejected without exposing input or zlib details`() {
        fixture.getValue("invalid").jsonArray.forEach { element ->
            val item = element.jsonObject
            val error = assertThrows(item.value("name"), DirectException::class.java) {
                CompactQrEnvelope.normalizeUserInput(item.value("compact"))
            }
            assertEquals("invalid_bundle", error.code)
            assertNull(error.cause)
        }
    }

    @Test fun `raw input is bounded before trimming and expanded output is bounded while inflating`() {
        assertThrows(DirectException::class.java) { CompactQrEnvelope.normalizeUserInput(" ".repeat(87_384) + "A") }
        val maximum = ByteArray(65_536) { 'A'.code.toByte() }
        assertEquals(encode(maximum), CompactQrEnvelope.normalizeUserInput(compact(maximum)))
        for (size in listOf(65_537, 1_048_576)) {
            assertThrows(DirectException::class.java) {
                CompactQrEnvelope.normalizeUserInput(compact(ByteArray(size) { 'A'.code.toByte() }))
            }
        }
    }

    @Test fun `decompression preserves original UTF8 and JSON spacing without reinterpretation`() {
        val original = " { \"name\" : \"caf\u00e9 \ud83d\udcf1\" }\n".toByteArray(Charsets.UTF_8)
        assertEquals(encode(original), CompactQrEnvelope.normalizeUserInput(compact(original)))
    }

    @Test fun `compact acceptance stays outside generic bundle and capability decoding`() {
        val encodedEnvelope = valid.first().value("compact")
        assertThrows(DirectException::class.java) { DirectBundles.decode(encodedEnvelope) }
        val malformedJson = compact(byteArrayOf(0xff.toByte()))
        assertEquals("invalid_utf8", assertThrows(DirectException::class.java) {
            DirectBundles.type(CompactQrEnvelope.normalizeUserInput(malformedJson))
        }.code)
    }

    @Test fun `normalized signed updates still verify original signatures and reject tampering`() {
        val wire = fixture("direct-v2-wire.json")
        val now = Instant.parse(wire.value("invitationExpiresAt")).minusSeconds(60)
        val invitation = DirectBundles.invitation(wire.value("invitation"), now)
        val record = DirectRecord(invitation.clientId, invitation.displayName, invitation.endpoint,
            "fixture-key", wire.value("csrPem"), invitation.caCertificatePem,
            pairingId = wire.value("pairingId"), generation = 1)
        val update = valid.first { DirectBundles.type(it.value("original")) == "mobile-egress-direct-endpoint-update" }
        val normalized = CompactQrEnvelope.normalizeUserInput(update.value("compact"))
        assertEquals(DirectBundles.endpoint(update.value("original"), record, now), DirectBundles.endpoint(normalized, record, now))

        val wrapper = DirectBundles.json.parseToJsonElement(DirectBundles.text(DirectBundles.decode(normalized))).jsonObject
        val signature = DirectBundles.decode(wrapper.value("signature"))
        signature[signature.lastIndex] = (signature.last().toInt() xor 1).toByte()
        val tampered = JsonObject(wrapper + ("signature" to JsonPrimitive(encode(signature)))).toString().toByteArray()
        assertEquals("invalid_update_signature", assertThrows(DirectException::class.java) {
            DirectBundles.endpoint(CompactQrEnvelope.normalizeUserInput(compact(tampered)), record, now)
        }.code)
    }

    private fun compact(bytes: ByteArray): String {
        val compressed = ByteArrayOutputStream()
        DeflaterOutputStream(compressed).use { it.write(bytes) }
        return "MEQR1:" + encode(compressed.toByteArray())
    }
    private fun encode(bytes: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    private fun JsonObject.value(name: String) = getValue(name).jsonPrimitive.content
    private fun fixture(name: String) = generateSequence(File(requireNotNull(System.getProperty("user.dir")))) { it.parentFile }
        .map { File(it, "testdata/$name") }.first { it.isFile }.readText()
        .let { DirectBundles.json.parseToJsonElement(it).jsonObject }
}
