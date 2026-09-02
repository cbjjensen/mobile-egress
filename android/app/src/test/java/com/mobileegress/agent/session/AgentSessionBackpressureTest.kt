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
