package com.mobileegress.agent.session

import android.net.Network
import android.util.Log
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
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
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

internal fun agentSessionUrl(relayOrigin: String) =
    relayOrigin.toHttpUrl().newBuilder()
        .addPathSegments("v1/session")
        .build()

internal fun relayFailureDiagnostic(error: Throwable, responseCode: Int?): String =
    "${error.javaClass.simpleName} http=${responseCode ?: "none"}"

class AgentSession(
    private val network: Network,
    private val identity: AgentIdentity,
    deviceKeyStore: DeviceKeyStore,
    parentScope: CoroutineScope,
    private val listener: AgentSessionListener,
) {
    private val job = SupervisorJob(parentScope.coroutineContext[Job])
    private val scope = CoroutineScope(parentScope.coroutineContext + job + Dispatchers.IO)
    private val outbound = OutboundMailbox()
    private val streams = java.util.concurrent.ConcurrentHashMap<String, TargetStream>()
    private val admission = StreamAdmission(AgentCapacity.MAX_STREAMS)
    private val closed = AtomicBoolean(false)
    private val tombstones = StreamTombstones()
    private val reactor = TargetIoReactor(
        binder = TargetSocketBinder(network::bindSocket),
        listener = ReactorListener(),
    )
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
        reactor.start()
    }

    fun connect() {
        if (closed.get()) return
        val sessionUrl = agentSessionUrl(identity.relayOrigin)
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
            Log.w(RELAY_LOG_TAG, "WebSocket closing code=$code")
            webSocket.close(code, null)
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            Log.w(RELAY_LOG_TAG, "WebSocket closed code=$code")
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }

        override fun onFailure(webSocket: WebSocket, error: Throwable, response: Response?) {
            Log.w(RELAY_LOG_TAG, relayFailureDiagnostic(error, response?.code))
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
        val stream = TargetStream(envelope.streamId)
        outbound.allowData(stream.id)
        streams[envelope.streamId] = stream
        publishStreamCount()
        when (reactor.open(stream.id, InetSocketAddress(address, target.port))) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.StreamLimit -> {
                streams.remove(stream.id, stream)
                admission.release(stream.id)
                reject(stream.id, "agent_stream_limit")
                publishStreamCount()
            }
            ReactorSubmitResult.SessionSaturated -> {
                terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            }
            ReactorSubmitResult.StreamSaturated,
            ReactorSubmitResult.MissingOrClosed -> {
                streams.remove(stream.id, stream)
                admission.release(stream.id)
                reject(stream.id, "target_failure")
                publishStreamCount()
            }
        }
    }

    private fun routeData(envelope: WireEnvelope) {
        val stream = streams[envelope.streamId]
            ?: throw ProtocolException("Data for an unknown stream")
        val payload = envelope.decodePayload()
        if (!stream.isOpen()) throw ProtocolException("Data for a closed stream")
        when (reactor.write(stream.id, payload)) {
            ReactorSubmitResult.Accepted,
            ReactorSubmitResult.StreamSaturated -> Unit
            ReactorSubmitResult.SessionSaturated -> {
                terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            }
            ReactorSubmitResult.StreamLimit,
            ReactorSubmitResult.MissingOrClosed -> throw ProtocolException("Data for a closed stream")
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
            val canceledPendingFrame = outbound.cancelStream(envelope.streamId)
            if (canceledPendingFrame || isTombstoned(envelope.streamId)) return
            throw ProtocolException("Close for an unknown stream")
        }
        closeStreamFromRelay(stream)
    }

    private fun reject(streamId: String, code: String) {
        rememberTombstone(streamId)
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
            outbound.offerRequiredControl(terminalFrame, streamId = stream.id) {}.also { accepted ->
                if (accepted) stream.terminalState = StreamTerminalState.ForcedPending
            }
        }
        if (!reserved) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            return
        }
        releaseStream(stream, errorClass)
    }

    private fun rejectUnopenedStream(
        stream: TargetStream,
        code: String,
        errorClass: ErrorClass,
    ) {
        val shouldReject = synchronized(stream.terminalLock) {
            if (stream.terminalState != StreamTerminalState.Open || closed.get()) return
            stream.terminalState = StreamTerminalState.Released
            true
        }
        if (!shouldReject) return
        outbound.blockAndDiscardData(stream.id)
        finalizeStreamRelease(stream, errorClass)
        reject(stream.id, code)
    }

    private fun closeStreamFromRelay(stream: TargetStream) {
        val waitForCancellation = synchronized(stream.terminalLock) {
            when (stream.terminalState) {
                StreamTerminalState.Released -> return
                StreamTerminalState.Open -> {
                    outbound.cancelStream(stream.id)
                    stream.terminalState = StreamTerminalState.CancelPending
                    true
                }
                StreamTerminalState.GracefulPending,
                StreamTerminalState.ForcedPending,
                StreamTerminalState.CancelPending -> {
                    outbound.cancelStream(stream.id)
                    stream.terminalState = StreamTerminalState.Released
                    false
                }
            }
        }
        val result = reactor.cancel(stream.id)
        if (result == ReactorSubmitResult.SessionSaturated) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            return
        }
        if (!waitForCancellation || result != ReactorSubmitResult.Accepted) {
            finalizeStreamRelease(stream, ErrorClass.None)
        }
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
        if (!shouldRelease) return
        if (reactor.release(stream.id) == ReactorSubmitResult.SessionSaturated) {
            terminate(ErrorClass.Backpressure, sendWebSocketClose = false)
            return
        }
        finalizeStreamRelease(stream, ErrorClass.None)
    }

    private fun finalizeStreamRelease(stream: TargetStream, errorClass: ErrorClass) {
        streams.remove(stream.id, stream)
        admission.release(stream.id)
        rememberTombstone(stream.id)
        AgentStatusBus.update {
            it.copy(
                activeStreams = admission.size,
                errorClass = if (errorClass == ErrorClass.None) it.errorClass else errorClass,
            )
        }
    }

    private fun trySendData(streamId: String, payload: ByteArray): Boolean =
        !closed.get() && outbound.offerData(streamId, WireProtocol.encode("data", streamId, payload))

    private fun enqueueRequiredControl(
        type: String,
        streamId: String = "",
        payload: ByteArray = byteArrayOf(),
    ): Boolean {
        if (closed.get()) return false
        return outbound.offerRequiredControl(
            WireProtocol.encode(type, streamId, payload),
            streamId = streamId.takeIf { it.isNotEmpty() },
        ) {
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
        }
        streams.clear()
        reactor.shutdown()
        job.cancel()
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
        AgentStatusBus.update { it.copy(relay = RelayHealth.Disconnected, activeStreams = 0) }
        listener.onTerminated(errorClass)
    }

    private fun publishStreamCount() {
        AgentStatusBus.update { it.copy(activeStreams = admission.size) }
    }

    private fun rememberTombstone(streamId: String) = tombstones.remember(streamId)

    private fun isTombstoned(streamId: String): Boolean = tombstones.contains(streamId)

    private class TargetStream(val id: String) {
        val terminalLock = Any()
        @Volatile var terminalState = StreamTerminalState.Open

        fun isOpen(): Boolean = terminalState == StreamTerminalState.Open
    }

    private inner class ReactorListener : TargetReactorListener {
        override fun onOpened(streamId: String) {
            val stream = streams[streamId] ?: return
            if (!stream.isOpen() || closed.get()) return
            enqueueRequiredControl("opened", streamId)
        }

        override fun onData(streamId: String, payload: ByteArray): Boolean {
            val stream = streams[streamId] ?: return false
            if (!stream.isOpen() || closed.get()) return false
            val accepted = trySendData(streamId, payload)
            if (accepted) AgentStatusBus.update { it.copy(bytesDown = it.bytesDown + payload.size) }
            return accepted
        }

        override fun onBytesWritten(streamId: String, byteCount: Int) {
            if (byteCount > 0 && streams.containsKey(streamId)) {
                AgentStatusBus.update { it.copy(bytesUp = it.bytesUp + byteCount) }
            }
        }

        override fun onTerminal(streamId: String, reason: TargetTerminalReason) {
            val stream = streams[streamId] ?: return
            if (closed.get()) return
            val action = reason.protocolAction()
            val code = action.code
            if (code == null) {
                if (reason == TargetTerminalReason.Canceled) releaseStream(stream, ErrorClass.None)
                return
            }
            if (action.rejectUnopenedStream) {
                rejectUnopenedStream(stream, code, action.errorClass)
            } else if (action.drainOutboundData) {
                closeStreamAfterDraining(stream, code)
            } else {
                failStream(stream, code, action.errorClass)
            }
        }

        override fun onFatalFailure() {
            terminate(ErrorClass.Internal, sendWebSocketClose = false)
        }
    }

    private enum class StreamTerminalState {
        Open,
        GracefulPending,
        ForcedPending,
        CancelPending,
        Released,
    }

    companion object {
        private const val RELAY_LOG_TAG = "AgentSession"
        private const val NORMAL_CLOSE = 1000
        private const val POLICY_VIOLATION_CLOSE = 1008
        private const val BACKPRESSURE_CLOSE_CODE = "agent_unavailable"
    }
}

object AgentCapacity {
    const val MAX_STREAMS = 256
    const val OUTBOUND_CONTROL_CAPACITY = 512
    const val OUTBOUND_DATA_CAPACITY = 256
    const val OUTBOUND_PER_STREAM_DATA_CAPACITY = 2
    const val REACTOR_COMMAND_CAPACITY = 512
    const val TARGET_INBOUND_PER_STREAM_CAPACITY = 2
    const val PREFERRED_TARGET_READ_BYTES = 16 * 1024
    const val RETAINED_STREAM_CAPACITY = 1_024
}
