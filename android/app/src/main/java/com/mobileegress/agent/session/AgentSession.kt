package com.mobileegress.agent.session

import android.net.Network
import com.mobileegress.agent.network.DestinationRejected
import com.mobileegress.agent.network.PublicAddressPolicy
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.protocol.ProtocolException
import com.mobileegress.agent.protocol.WireEnvelope
import com.mobileegress.agent.protocol.WireProtocol
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.PinnedTls
import com.mobileegress.agent.status.AgentStatusBus
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import java.net.InetSocketAddress
import java.net.Socket
import java.util.ArrayDeque
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

interface AgentSessionListener {
    fun onConnected()
    fun onTerminated(errorClass: ErrorClass)
}

class AgentSession(
    private val network: Network,
    private val identity: AgentIdentity,
    deviceKeyStore: DeviceKeyStore,
    parentScope: CoroutineScope,
    private val listener: AgentSessionListener,
) {
    private val job = SupervisorJob(parentScope.coroutineContext[Job])
    private val scope = CoroutineScope(parentScope.coroutineContext + job + Dispatchers.IO)
    private val outbound = OutboundMailbox(
        controlCapacity = OUTBOUND_CONTROL_QUEUE_CAPACITY,
        dataCapacity = OUTBOUND_DATA_QUEUE_CAPACITY,
        perStreamDataCapacity = OUTBOUND_PER_STREAM_DATA_QUEUE_CAPACITY,
    )
    private val streams = java.util.concurrent.ConcurrentHashMap<String, TargetStream>()
    private val admission = StreamAdmission(MAX_AGENT_STREAMS)
    private val closed = AtomicBoolean(false)
    private val tombstoneLock = Any()
    private val tombstones = ArrayDeque<String>()
    private val tombstoneSet = HashSet<String>()
    private val client: OkHttpClient
    @Volatile private var webSocket: WebSocket? = null

    init {
        require(identity.role == "agent")
        val ca = PairingBundleParser.parseCaCertificate(identity.caCertificatePem)
        val privateKey = requireNotNull(deviceKeyStore.privateKey(identity.keyAlias)) {
            "AndroidKeyStore Agent key is unavailable"
        }
        val trustManager = PinnedTls.trustManager(ca)
        val keyManager = PinnedTls.deviceKeyManager(identity, privateKey)
        client = PinnedTls.clientBuilder(network, trustManager, keyManager)
            .connectTimeout(10, TimeUnit.SECONDS)
            .writeTimeout(5, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(20, TimeUnit.SECONDS)
            .build()
    }

    fun connect() {
        if (closed.get()) return
        val sessionUrl = identity.relayOrigin.toHttpUrl().newBuilder()
            .scheme("wss")
            .addPathSegments("v1/session")
            .build()
        val request = Request.Builder().url(sessionUrl).build()
        webSocket = client.newWebSocket(request, SocketListener())
    }

    fun close() {
        terminate(ErrorClass.None, sendWebSocketClose = true)
    }

    private inner class SocketListener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            if (closed.get()) {
                webSocket.close(NORMAL_CLOSE, "session_closed")
                return
            }
            listener.onConnected()
            scope.launch { writeLoop(webSocket) }
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            protocolFailure(webSocket)
        }

        override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
            try {
                handleEnvelope(WireProtocol.parseAgentInbound(bytes.toByteArray()))
            } catch (_: ProtocolException) {
                protocolFailure(webSocket)
            } catch (_: Exception) {
                terminate(ErrorClass.Internal, sendWebSocketClose = false)
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(code, null)
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }

        override fun onFailure(webSocket: WebSocket, error: Throwable, response: Response?) {
            val errorClass = when {
                response?.code == 401 || response?.code == 403 -> ErrorClass.RelayAuth
                error is javax.net.ssl.SSLException -> ErrorClass.RelayTls
                else -> ErrorClass.RelayUnavailable
            }
            terminate(errorClass, sendWebSocketClose = false)
        }
    }

    private suspend fun writeLoop(socket: WebSocket) {
        try {
            while (true) {
                val message = outbound.receive() ?: return
                when (outbound.emit(message) { socket.send(it.toByteString()) }) {
                    OutboundEmission.Emitted,
                    OutboundEmission.Canceled -> Unit
                    OutboundEmission.Failed -> {
                        terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
                        return
                    }
                }
            }
        } catch (_: Exception) {
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }
    }

    private fun handleEnvelope(envelope: WireEnvelope) {
        when (envelope.type) {
            "ping" -> enqueueRequiredControl("pong")
            "pong" -> Unit
            "open" -> openStream(envelope)
            "data" -> routeData(envelope)
            "close" -> closeFromRelay(envelope)
            else -> throw ProtocolException("Role-incompatible Agent message")
        }
    }

    private fun openStream(envelope: WireEnvelope) {
        if (!admission.tryReserve(envelope.streamId)) {
            reject(envelope.streamId, "agent_stream_limit")
            return
        }
        val target = try {
            WireProtocol.parseOpen(envelope)
        } catch (error: ProtocolException) {
            admission.release(envelope.streamId)
            reject(envelope.streamId, "invalid_target")
            return
        }
        val address = try {
            PublicAddressPolicy.validate(target.ip, target.port)
        } catch (_: DestinationRejected) {
            admission.release(envelope.streamId)
            AgentStatusBus.update { it.copy(errorClass = ErrorClass.TargetPolicy) }
            reject(envelope.streamId, "policy_denied")
            return
        }
        val socket = try {
            network.socketFactory.createSocket()
        } catch (_: Exception) {
            admission.release(envelope.streamId)
            reject(envelope.streamId, "target_failure")
            return
        }
        val stream = TargetStream(envelope.streamId, socket)
        outbound.allowData(stream.id)
        streams[envelope.streamId] = stream
        publishStreamCount()
        scope.launch {
            try {
                stream.socket.connect(InetSocketAddress(address, target.port), TARGET_CONNECT_TIMEOUT_MILLIS)
                stream.socket.tcpNoDelay = true
                if (!stream.isOpen() || closed.get()) return@launch
                if (!enqueueRequiredControl("opened", stream.id)) return@launch
                stream.reader = launch { targetReadLoop(stream) }
                stream.writer = launch { targetWriteLoop(stream) }
            } catch (_: Exception) {
                failStream(stream, "target_failure", ErrorClass.TargetConnect)
            }
        }
    }

    private fun routeData(envelope: WireEnvelope) {
        val stream = streams[envelope.streamId]
            ?: throw ProtocolException("Data for an unknown stream")
        val payload = envelope.decodePayload()
        if (stream.inbound.trySend(payload).isFailure) {
            failStream(stream, BACKPRESSURE_CLOSE_CODE, ErrorClass.Backpressure)
        }
    }

    private fun closeFromRelay(envelope: WireEnvelope) {
        val code = try {
            envelope.decodePayload().decodeToString(throwOnInvalidSequence = true)
        } catch (_: Exception) {
            throw ProtocolException("Invalid close code")
        }
        WireProtocol.finiteErrorCode(code)
        val stream = streams[envelope.streamId]
        if (stream == null) {
            if (isTombstoned(envelope.streamId)) return
            throw ProtocolException("Close for an unknown stream")
        }
        closeStreamFromRelay(stream)
    }

    private suspend fun targetReadLoop(stream: TargetStream) {
        val buffer = ByteArray(STREAM_CHUNK_BYTES)
        try {
            val input = stream.socket.getInputStream()
            while (scope.isActive && stream.isOpen()) {
                val read = input.read(buffer)
                if (read < 0) break
                if (read == 0) continue
                val chunk = buffer.copyOf(read)
                if (!trySendData(stream.id, chunk)) {
                    failStream(stream, BACKPRESSURE_CLOSE_CODE, ErrorClass.Backpressure)
                    return
                }
                AgentStatusBus.update { it.copy(bytesDown = it.bytesDown + read) }
            }
            closeStreamAfterDraining(stream, "target_closed")
        } catch (_: Exception) {
            failStream(stream, "target_failure", ErrorClass.TargetConnect)
        }
    }

    private suspend fun targetWriteLoop(stream: TargetStream) {
        try {
            val output = stream.socket.getOutputStream()
            for (payload in stream.inbound) {
                output.write(payload)
                output.flush()
                AgentStatusBus.update { it.copy(bytesUp = it.bytesUp + payload.size) }
            }
        } catch (_: Exception) {
            failStream(stream, "target_failure", ErrorClass.TargetConnect)
        }
    }

    private fun reject(streamId: String, code: String) {
        enqueueRequiredControl("rejected", streamId, WireProtocol.finiteErrorCode(code))
    }

    private fun closeStreamAfterDraining(stream: TargetStream, code: String) {
        val terminalFrame = WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code))
        val reserved = synchronized(stream.terminalLock) {
            if (stream.terminalState != StreamTerminalState.Open || closed.get()) return
            outbound.offerRequiredControlAfterData(
                stream.id,
                terminalFrame,
                onEmitted = { onGracefulCloseEmitted(stream) },
            ) {}.also { accepted ->
                if (accepted) stream.terminalState = StreamTerminalState.GracefulPending
            }
        }
        if (!reserved) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            return
        }
    }

    private fun failStream(
        stream: TargetStream,
        code: String,
        errorClass: ErrorClass,
    ) {
        val terminalFrame = WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code))
        val reserved = synchronized(stream.terminalLock) {
            if (stream.terminalState != StreamTerminalState.Open || closed.get()) return
            outbound.blockAndDiscardData(stream.id)
            outbound.offerRequiredControl(terminalFrame) {}.also { accepted ->
                if (accepted) stream.terminalState = StreamTerminalState.ForcedPending
            }
        }
        if (!reserved) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            return
        }
        releaseStream(stream, errorClass)
    }

    private fun closeStreamFromRelay(stream: TargetStream) {
        val shouldRelease = synchronized(stream.terminalLock) {
            when (stream.terminalState) {
                StreamTerminalState.Open -> outbound.blockAndDiscardData(stream.id)
                StreamTerminalState.GracefulPending -> outbound.cancelGracefulStream(stream.id)
                StreamTerminalState.ForcedPending -> Unit
                StreamTerminalState.Released -> return
            }
            stream.terminalState = StreamTerminalState.Released
            true
        }
        if (shouldRelease) finalizeStreamRelease(stream, ErrorClass.None)
    }

    private fun releaseStream(stream: TargetStream, errorClass: ErrorClass) {
        val shouldRelease = synchronized(stream.terminalLock) {
            if (stream.terminalState == StreamTerminalState.Released) return
            stream.terminalState = StreamTerminalState.Released
            true
        }
        if (shouldRelease) finalizeStreamRelease(stream, errorClass)
    }

    private fun onGracefulCloseEmitted(stream: TargetStream) {
        val shouldRelease = synchronized(stream.terminalLock) {
            if (stream.terminalState != StreamTerminalState.GracefulPending) return
            stream.terminalState = StreamTerminalState.Released
            true
        }
        if (shouldRelease) finalizeStreamRelease(stream, ErrorClass.None)
    }

    private fun finalizeStreamRelease(stream: TargetStream, errorClass: ErrorClass) {
        streams.remove(stream.id, stream)
        admission.release(stream.id)
        rememberTombstone(stream.id)
        stopStreamIo(stream)
        AgentStatusBus.update {
            it.copy(
                activeStreams = admission.size,
                errorClass = if (errorClass == ErrorClass.None) it.errorClass else errorClass,
            )
        }
    }

    private fun stopStreamIo(stream: TargetStream) {
        stream.inbound.close()
        try {
            stream.socket.close()
        } catch (_: Exception) {
            // Stream teardown is best effort and contains no reportable detail.
        }
        stream.reader?.cancel()
        stream.writer?.cancel()
    }

    private fun trySendData(streamId: String, payload: ByteArray): Boolean =
        !closed.get() && outbound.offerData(streamId, WireProtocol.encode("data", streamId, payload))

    private fun enqueueRequiredControl(
        type: String,
        streamId: String = "",
        payload: ByteArray = byteArrayOf(),
    ): Boolean {
        if (closed.get()) return false
        return outbound.offerRequiredControl(WireProtocol.encode(type, streamId, payload)) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
        }
    }

    private fun protocolFailure(socket: WebSocket) {
        socket.close(POLICY_VIOLATION_CLOSE, "protocol_error")
        terminate(ErrorClass.Protocol, sendWebSocketClose = false)
    }

    private fun terminate(errorClass: ErrorClass, sendWebSocketClose: Boolean) {
        if (!closed.compareAndSet(false, true)) return
        if (sendWebSocketClose) webSocket?.close(NORMAL_CLOSE, "session_closed") else webSocket?.cancel()
        outbound.close()
        admission.clear()
        streams.values.toList().forEach { stream ->
            synchronized(stream.terminalLock) {
                stream.terminalState = StreamTerminalState.Released
            }
            stopStreamIo(stream)
        }
        streams.clear()
        job.cancel()
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
        AgentStatusBus.update { it.copy(relay = RelayHealth.Disconnected, activeStreams = 0) }
        listener.onTerminated(errorClass)
    }

    private fun publishStreamCount() {
        AgentStatusBus.update { it.copy(activeStreams = admission.size) }
    }

    private fun rememberTombstone(streamId: String) = synchronized(tombstoneLock) {
        if (!tombstoneSet.add(streamId)) return@synchronized
        tombstones.addLast(streamId)
        if (tombstones.size > MAX_TOMBSTONES) {
            tombstoneSet.remove(tombstones.removeFirst())
        }
    }

    private fun isTombstoned(streamId: String): Boolean = synchronized(tombstoneLock) {
        streamId in tombstoneSet
    }

    private class TargetStream(val id: String, val socket: Socket) {
        val inbound = Channel<ByteArray>(capacity = STREAM_INBOUND_QUEUE_CAPACITY)
        val terminalLock = Any()
        @Volatile var terminalState = StreamTerminalState.Open
        @Volatile var reader: Job? = null
        @Volatile var writer: Job? = null

        fun isOpen(): Boolean = terminalState == StreamTerminalState.Open
    }

    private enum class StreamTerminalState { Open, GracefulPending, ForcedPending, Released }

    companion object {
        private const val MAX_AGENT_STREAMS = 8
        private const val MAX_TOMBSTONES = 32
        private const val OUTBOUND_CONTROL_QUEUE_CAPACITY = 32
        private const val OUTBOUND_DATA_QUEUE_CAPACITY = 64
        private const val OUTBOUND_PER_STREAM_DATA_QUEUE_CAPACITY = 8
        private const val STREAM_INBOUND_QUEUE_CAPACITY = 4
        private const val STREAM_CHUNK_BYTES = 32 * 1024
        private const val TARGET_CONNECT_TIMEOUT_MILLIS = 30_000
        private const val NORMAL_CLOSE = 1000
        private const val POLICY_VIOLATION_CLOSE = 1008
        private const val BACKPRESSURE_CLOSE_CODE = "agent_unavailable"
    }
}
