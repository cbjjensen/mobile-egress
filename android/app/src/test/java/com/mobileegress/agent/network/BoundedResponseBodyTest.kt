package com.mobileegress.agent.network

import okio.Buffer
import okio.ForwardingSource
import okio.buffer
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class BoundedResponseBodyTest {
    @Test
    fun `short response returns without requiring the maximum byte count`() {
        val response = "{\"ready\":true}".encodeToByteArray()

        val actual = readBoundedResponseBody(
            source = Buffer().write(response),
            maximumBytes = 256 * 1024,
        )

        assertArrayEquals(response, actual)
    }

    @Test
    fun `response at the maximum is accepted`() {
        val response = ByteArray(16) { it.toByte() }

        val actual = readBoundedResponseBody(
            source = Buffer().write(response),
            maximumBytes = 16,
        )

        assertArrayEquals(response, actual)
    }

    @Test
    fun `response larger than the maximum is rejected`() {
        val source = Buffer().write(ByteArray(17) { it.toByte() })

        assertThrows(ResponseBodyTooLargeException::class.java) {
            readBoundedResponseBody(source = source, maximumBytes = 16)
        }
    }

    @Test
    fun `chunked response is read until the source is exhausted`() {
        val response = "{\"relayUrl\":\"https://relay.example\"}".encodeToByteArray()
        val upstream = Buffer().write(response)
        val source = object : ForwardingSource(upstream) {
            override fun read(sink: Buffer, byteCount: Long): Long =
                super.read(sink, minOf(byteCount, 3L))
        }.buffer()

        val actual = readBoundedResponseBody(source = source, maximumBytes = 256 * 1024)

        assertArrayEquals(response, actual)
    }
}
