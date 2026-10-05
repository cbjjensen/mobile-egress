package com.mobileegress.agent.pairing

import com.mobileegress.agent.direct.DirectBundles
import com.mobileegress.agent.direct.DirectException
import java.util.Base64
import java.util.zip.DataFormatException
import java.util.zip.Inflater

/** Normalization is confined to owner-imported QR and pasted setup codes. */
object CompactQrEnvelope {
    private const val PREFIX = "MEQR1:"
    private const val MAX_INPUT_CHARS = 87_384
    private const val MAX_EXPANDED_BYTES = 65_536

    fun normalizeUserInput(input: String): String {
        if (input.length > MAX_INPUT_CHARS) throw DirectException("invalid_bundle")
        val value = input.trim()
        if (!value.startsWith("MEQR")) return value
        if (!value.startsWith(PREFIX)) throw DirectException("invalid_bundle")

        // Reuse strict canonical base64url and the existing 64-KiB byte bound;
        // generic bundle/capability decoding itself remains envelope-unaware.
        val compressed = DirectBundles.decode(value.substring(PREFIX.length))
        val inflater = Inflater()
        try {
            inflater.setInput(compressed)
            // One extra byte detects overflow before allocating or returning more.
            val output = ByteArray(MAX_EXPANDED_BYTES + 1)
            var count = 0
            while (!inflater.finished()) {
                val written = inflater.inflate(output, count, output.size - count)
                count += written
                if (count > MAX_EXPANDED_BYTES || inflater.needsDictionary() ||
                    (written == 0 && !inflater.finished())) throw DirectException("invalid_bundle")
            }
            if (count == 0 || inflater.remaining != 0) throw DirectException("invalid_bundle")
            return Base64.getUrlEncoder().withoutPadding().encodeToString(output.copyOf(count))
        } catch (_: DataFormatException) {
            throw DirectException("invalid_bundle")
        } finally {
            inflater.end()
        }
    }
}
