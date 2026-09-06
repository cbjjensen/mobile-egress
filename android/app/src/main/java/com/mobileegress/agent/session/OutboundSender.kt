package com.mobileegress.agent.session

import java.util.ArrayDeque
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withTimeoutOrNull

/** The session's sole WebSocket message producer. OkHttp queueSize counts encoded message
 * bytes until writeMessageFrame completes, so enqueue success must not refund mailbox debt. */
internal class OutboundSender(
    private val mailbox: OutboundMailbox,
    private val send: (ByteArray) -> Boolean,
    private val queueSize: () -> Long,
    private val windowBytes: Long = 512 * 1024,
    private val reservedControlBytes: Long = 64 * 1024,
) {
    private val inFlight = ArrayDeque<OutboundFrame>()
    private var inFlightBytes = 0L
    private var inFlightDataBytes = 0L

    init {
        require(windowBytes > 0)
        require(reservedControlBytes in 0 until windowBytes)
    }

    fun pump(checkActive: () -> Unit = {}): Boolean {
        while (true) {
            checkActive()
            reconcile()
            val totalAvailable = windowBytes - inFlightBytes
            val dataAvailable = minOf(totalAvailable, windowBytes - reservedControlBytes - inFlightDataBytes)
            val frame = mailbox.poll(dataAvailable, totalAvailable) ?: return true
            if (mailbox.emit(frame, retainUntilDrained = true) { bytes ->
                    if (!send(bytes)) {
                        false
                    } else {
                        inFlight.addLast(frame)
                        inFlightBytes += bytes.size
                        inFlightDataBytes += frame.dataByteCount
                        true
                    }
                } == OutboundEmission.Failed
            ) return false
        }
    }

    suspend fun run(): Boolean {
        try {
            val context = currentCoroutineContext()
            while (true) {
                if (!pump { context.ensureActive() }) return false
                // New controls wake a saturated writer immediately. Only SDK work requires
                // timed checks; a fully idle writer sleeps on the existing mailbox signal.
                val available = if (inFlight.isEmpty()) {
                    mailbox.awaitAvailable()
                } else {
                    withTimeoutOrNull(2) { mailbox.awaitAvailable() }
                }
                if (available == false) return true
            }
        } finally {
            close()
        }
    }

    private fun reconcile() {
        if (inFlight.isEmpty()) return
        var completedBytes = inFlightBytes - queueSize()
        check(completedBytes >= 0) { "WebSocket message queue has another producer" }
        while (inFlight.isNotEmpty() && inFlight.first.bytes.size <= completedBytes) {
            val frame = inFlight.removeFirst()
            completedBytes -= frame.bytes.size
            inFlightBytes -= frame.bytes.size
            inFlightDataBytes -= frame.dataByteCount
            mailbox.drained(frame)
        }
    }

    fun close() {
        mailbox.close()
        inFlight.clear()
        inFlightBytes = 0
        inFlightDataBytes = 0
    }
}
