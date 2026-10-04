package com.mobileegress.agent.session

import android.net.Network
import com.mobileegress.agent.protocol.WireEnvelope
import com.mobileegress.agent.protocol.WireProtocol
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.status.ErrorClass
import java.security.KeyPairGenerator
import java.util.Collections
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentSessionBackpressureTest {
    @Test fun `live update commits once before reconnect and is bounded`() {
        val events = mutableListOf<String>()
        val parentJob = SupervisorJob()
        val session = AgentSession(testNetwork(), AgentIdentity("https://client.example", "agent", "AB", "key", "cert", "ca"),
            DeviceKeyStore(), CoroutineScope(parentJob), object : AgentSessionListener {
                override val supportsEndpointUpdates = true
                override fun onConnected() = Unit
                override fun onEndpointUpdate(bundle: String): Boolean { events += bundle; return true }
                override fun onTerminated(errorClass: ErrorClass) { events += errorClass.name }
            }, privateKeyProvider = { testPrivateKey() }, clientFactory = { _, _, _ -> OkHttpClient() })
        try {
            val raw = WireProtocol.encode("endpoint_update", payload = "signed-bundle".encodeToByteArray())
            val envelope = WireProtocol.parseAgentInbound(raw)
            val handle = AgentSession::class.java.getDeclaredMethod("handleEnvelope", WireEnvelope::class.java).apply { isAccessible = true }
            handle.invoke(session, envelope); handle.invoke(session, envelope)
            assertEquals(listOf("signed-bundle", "None"), events)
            org.junit.Assert.assertThrows(com.mobileegress.agent.protocol.ProtocolException::class.java) {
                WireProtocol.encode("endpoint_update", payload = ByteArray(87_385))
            }
        } finally { parentJob.cancel() }
    }
    @Test
    fun `pong required control saturation reports its safe source and terminates the real session`() {
        val reports = Collections.synchronizedList(mutableListOf<BackpressureSource>())
        val terminations = Collections.synchronizedList(mutableListOf<ErrorClass>())
        val parentJob = SupervisorJob()
        val session = AgentSession(
            network = testNetwork(),
            identity = AgentIdentity(
                relayOrigin = "https://relay.example",
                role = "agent",
                serial = "serial",
                keyAlias = "test-key",
                certificatePem = "unused",
                caCertificatePem = "unused",
            ),
            deviceKeyStore = DeviceKeyStore(),
            parentScope = CoroutineScope(parentJob),
            listener = object : AgentSessionListener {
                override fun onConnected() = Unit
                override fun onTerminated(errorClass: ErrorClass) {
                    terminations += errorClass
                }
            },
            outbound = OutboundMailbox(controlCapacity = 1),
            backpressureReporter = reports::add,
            privateKeyProvider = { testPrivateKey() },
            clientFactory = { _, _, _ -> OkHttpClient() },
        )

        try {
            session.handlePingForTest()
            session.handlePingForTest()

            assertEquals(listOf(BackpressureSource.RequiredControlSaturation), reports)
            assertEquals(listOf(ErrorClass.Backpressure), terminations)
        } finally {
            parentJob.cancel()
        }
    }

    private fun AgentSession.handlePingForTest() {
        AgentSession::class.java
            .getDeclaredMethod("handleEnvelope", WireEnvelope::class.java)
            .apply { isAccessible = true }
            .invoke(this, WireProtocol.parseAgentInbound(WireProtocol.encode("ping")))
    }

    private fun testPrivateKey() = KeyPairGenerator.getInstance("EC").apply {
        initialize(256)
    }.generateKeyPair().private

    private fun testNetwork(): Network = Network::class.java
        .getDeclaredConstructor()
        .apply { isAccessible = true }
        .newInstance()
}
