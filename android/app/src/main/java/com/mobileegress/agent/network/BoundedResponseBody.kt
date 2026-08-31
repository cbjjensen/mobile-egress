package com.mobileegress.agent.network

import okio.Buffer
import okio.BufferedSource

internal class ResponseBodyTooLargeException : IllegalStateException("Response body exceeds the configured limit")

internal fun readBoundedResponseBody(
    source: BufferedSource,
    maximumBytes: Int,
): ByteArray {
    val buffer = Buffer()
    val readLimit = maximumBytes.toLong() + 1L
    while (buffer.size < readLimit) {
        if (source.read(buffer, readLimit - buffer.size) == -1L) break
    }
    if (buffer.size > maximumBytes) {
        throw ResponseBodyTooLargeException()
    }
    return buffer.readByteArray()
}
