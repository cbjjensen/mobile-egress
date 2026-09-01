package com.mobileegress.agent.session

import android.net.Network
import android.util.Log
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
    private val closed = AtomicBoolean(false)
    private val client: OkHttpClient
    private val targetBridge: AgentTargetBridge
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
        targetBridge = AgentTargetBridge(
            outbound = outbound,
            reactorFactory = { reactorListener ->
                TargetIoReactor(
                    binder = TargetSocketBinder(network::bindSocket),
                    listener = reactorListener,
                )
            },
            onSessionFailure = { errorClass ->
                terminate(errorClass, sendWebSocketClose = false)
            },
            status = object : AgentTargetStatusSink {
                override fun onActiveStreams(count: Int) {
                    AgentStatusBus.update { it.copy(activeStreams = count) }
                }

                override fun onBytesDown(byteCount: Int) {
                    AgentStatusBus.update { it.copy(bytesDown = it.bytesDown + byteCount) }
                }

                override fun onBytesUp(byteCount: Int) {
                    AgentStatusBus.update { it.copy(bytesUp = it.bytesUp + byteCount) }
                }

                override fun onError(errorClass: ErrorClass) {
                    AgentStatusBus.update { it.copy(errorClass = errorClass) }
                }
            },
        )
    }

    fun connect() {
        if (closed.get()) return
        val sessionUrl = agentSessionUrl(identity.relayOrigin)
        val request = Request.Builder().url(sessionUrl).build()
        if (!targetBridge.start() || closed.get()) return
        try {
            webSocket = client.newWebSocket(request, SocketListener())
        } catch (_: Exception) {
            terminate(ErrorClass.RelayUnavailable, sendWebSocketClose = false)
        }
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
        targetBridge.open(envelope)
    }

    private fun routeData(envelope: WireEnvelope) {
        val payload = envelope.decodePayload()
        targetBridge.routeData(envelope.streamId, payload)
    }

    private fun closeFromRelay(envelope: WireEnvelope) {
        val code = try {
            envelope.decodePayload().decodeToString(throwOnInvalidSequence = true)
        } catch (_: Exception) {
            throw ProtocolException("Invalid close code")
        }
        WireProtocol.finiteErrorCode(code)
        targetBridge.closeFromRelay(envelope.streamId)
    }

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
        targetBridge.shutdownAndAwait(REACTOR_SHUTDOWN_TIMEOUT_MILLIS, TimeUnit.MILLISECONDS)
        outbound.close()
        job.cancel()
        client.connectionPool.evictAll()
        client.dispatcher.executorService.shutdown()
        AgentStatusBus.update { it.copy(relay = RelayHealth.Disconnected, activeStreams = 0) }
        listener.onTerminated(errorClass)
    }

    companion object {
        private const val RELAY_LOG_TAG = "AgentSession"
        private const val NORMAL_CLOSE = 1000
        private const val POLICY_VIOLATION_CLOSE = 1008
        private const val REACTOR_SHUTDOWN_TIMEOUT_MILLIS = 2_000L
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
