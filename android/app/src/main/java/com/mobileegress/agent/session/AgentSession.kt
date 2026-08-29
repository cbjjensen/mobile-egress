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
    private val outbound = Channel<ByteArray>(capacity = OUTBOUND_QUEUE_CAPACITY)
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
        for (message in outbound) {
            if (!socket.send(message.toByteString())) {
                terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
                return
            }
        }
    }

    private fun handleEnvelope(envelope: WireEnvelope) {
        when (envelope.type) {
            "ping" -> if (!enqueue("pong")) terminate(ErrorClass.Backpressure, false)
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
        streams[envelope.streamId] = stream
        publishStreamCount()
        scope.launch {
            try {
                stream.socket.connect(InetSocketAddress(address, target.port), TARGET_CONNECT_TIMEOUT_MILLIS)
                stream.socket.tcpNoDelay = true
                if (stream.closed.get() || closed.get()) return@launch
                if (!enqueue("opened", stream.id)) {
                    closeStream(stream, "target_failure", notifyRelay = false, ErrorClass.Backpressure)
                    return@launch
                }
                stream.reader = launch { targetReadLoop(stream) }
                stream.writer = launch { targetWriteLoop(stream) }
            } catch (_: Exception) {
                closeStream(stream, "target_failure", notifyRelay = true, ErrorClass.TargetConnect)
            }
        }
    }

    private fun routeData(envelope: WireEnvelope) {
        val stream = streams[envelope.streamId]
            ?: throw ProtocolException("Data for an unknown stream")
        val payload = envelope.decodePayload()
        if (stream.inbound.trySend(payload).isFailure) {
            closeStream(stream, "target_failure", notifyRelay = true, ErrorClass.Backpressure)
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
        closeStream(stream, code, notifyRelay = false, ErrorClass.None)
    }

    private suspend fun targetReadLoop(stream: TargetStream) {
        val buffer = ByteArray(STREAM_CHUNK_BYTES)
        try {
            val input = stream.socket.getInputStream()
            while (scope.isActive && !stream.closed.get()) {
                val read = input.read(buffer)
                if (read < 0) break
                if (read == 0) continue
                val chunk = buffer.copyOf(read)
                if (!enqueue("data", stream.id, chunk)) {
                    closeStream(stream, "target_failure", notifyRelay = true, ErrorClass.Backpressure)
                    return
                }
                AgentStatusBus.update { it.copy(bytesDown = it.bytesDown + read) }
            }
            closeStream(stream, "target_closed", notifyRelay = true, ErrorClass.None)
        } catch (_: Exception) {
            closeStream(stream, "target_failure", notifyRelay = true, ErrorClass.TargetConnect)
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
            closeStream(stream, "target_failure", notifyRelay = true, ErrorClass.TargetConnect)
        }
    }

    private fun reject(streamId: String, code: String) {
        if (!enqueue("rejected", streamId, WireProtocol.finiteErrorCode(code))) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
        }
    }

    private fun closeStream(
        stream: TargetStream,
        code: String,
        notifyRelay: Boolean,
        errorClass: ErrorClass,
    ) {
        if (!stream.closed.compareAndSet(false, true)) return
        streams.remove(stream.id, stream)
        admission.release(stream.id)
        rememberTombstone(stream.id)
        stream.inbound.close()
        try {
            stream.socket.close()
        } catch (_: Exception) {
            // Stream teardown is best effort and contains no reportable detail.
        }
        stream.reader?.cancel()
        stream.writer?.cancel()
        if (notifyRelay && !closed.get()) {
            enqueue("close", stream.id, WireProtocol.finiteErrorCode(code))
        }
        AgentStatusBus.update {
            it.copy(
                activeStreams = admission.size,
                errorClass = if (errorClass == ErrorClass.None) it.errorClass else errorClass,
            )
        }
    }

    private fun enqueue(type: String, streamId: String = "", payload: ByteArray = byteArrayOf()): Boolean =
        !closed.get() && outbound.trySend(WireProtocol.encode(type, streamId, payload)).isSuccess

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
            if (stream.closed.compareAndSet(false, true)) {
                stream.inbound.close()
                try {
                    stream.socket.close()
                } catch (_: Exception) {
                    // Session teardown is best effort.
                }
            }
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
        val closed = AtomicBoolean(false)
        @Volatile var reader: Job? = null
        @Volatile var writer: Job? = null
    }

    companion object {
        private const val MAX_AGENT_STREAMS = 8
        private const val MAX_TOMBSTONES = 32
        private const val OUTBOUND_QUEUE_CAPACITY = 64
        private const val STREAM_INBOUND_QUEUE_CAPACITY = 4
        private const val STREAM_CHUNK_BYTES = 32 * 1024
        private const val TARGET_CONNECT_TIMEOUT_MILLIS = 30_000
        private const val NORMAL_CLOSE = 1000
        private const val POLICY_VIOLATION_CLOSE = 1008
    }
}
