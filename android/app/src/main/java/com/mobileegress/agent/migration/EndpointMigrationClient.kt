package com.mobileegress.agent.migration

import android.net.Network
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.network.ResponseBodyTooLargeException
import com.mobileegress.agent.network.readBoundedResponseBody
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.PinnedTls
import java.util.concurrent.TimeUnit
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

class EndpointMigrationClient {
    private val json = Json { ignoreUnknownKeys = false; explicitNulls = true; isLenient = false }

    fun consume(
        migration: EndpointMigration,
        identity: AgentIdentity,
        privateKey: java.security.PrivateKey,
        network: Network,
    ): String {
        val ca = PairingBundleParser.parseCaCertificate(migration.caCertificatePem)
        val trust = PinnedTls.trustManager(ca)
        val keyManager = PinnedTls.deviceKeyManager(identity, privateKey)
        val client = PinnedTls.clientBuilder(network, trust, keyManager)
            .callTimeout(30, TimeUnit.SECONDS).build()
        try {
            val body = json.encodeToString(MigrationConsumeRequest(migration.capability))
                .toRequestBody(JSON_MEDIA_TYPE)
            val request = Request.Builder()
                .url(migration.relayOrigin + CONSUME_PATH)
                .post(body)
                .build()
            client.newCall(request).execute().use { response ->
                if (response.code != 200) throw EndpointMigrationException("Relay rejected endpoint migration")
                val source = response.body?.source()
                    ?: throw EndpointMigrationException("Relay returned an empty migration response")
                val raw = try {
                    readBoundedResponseBody(source, MAX_CONTROL_BYTES)
                } catch (_: ResponseBodyTooLargeException) {
                    throw EndpointMigrationException("Migration response is too large")
                }
                val result = try {
                    json.decodeFromString<MigrationConsumeResponse>(raw.decodeToString())
                } catch (error: SerializationException) {
                    throw EndpointMigrationException("Migration response is invalid", error)
                }
                return result.relayUrl
            }
        } catch (error: EndpointMigrationException) {
            throw error
        } catch (error: Exception) {
            throw EndpointMigrationException("Pinned cellular migration connection failed", error)
        } finally {
            client.connectionPool.evictAll()
            client.dispatcher.executorService.shutdown()
        }
    }

    companion object {
        private const val CONSUME_PATH = "/v1/endpoint-migrations/consume"
        private const val MAX_CONTROL_BYTES = 256 * 1024
        private val JSON_MEDIA_TYPE = "application/json".toMediaType()
    }
}

class CellularEndpointMigrationPerformer(
    private val networkAcquirer: CellularNetworkAcquirer,
    private val keyStore: DeviceKeyStore,
    private val client: EndpointMigrationClient,
) : EndpointMigrationPerformer {
    override suspend fun consume(migration: EndpointMigration, identity: AgentIdentity): String =
        networkAcquirer.acquire().use { lease ->
            val privateKey = keyStore.privateKey(identity.keyAlias)
                ?: throw EndpointMigrationException("Existing AndroidKeyStore key is unavailable")
            client.consume(migration, identity, privateKey, lease.network)
        }
}

@Serializable
private data class MigrationConsumeRequest(val capability: String)

@Serializable
private data class MigrationConsumeResponse(val relayUrl: String)
