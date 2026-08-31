package com.mobileegress.agent.network

import android.net.Network
import java.net.Inet6Address
import java.net.InetAddress
import java.util.concurrent.TimeUnit
import javax.net.SocketFactory
import kotlin.coroutines.cancellation.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withContext
import okhttp3.Dns
import okhttp3.OkHttpClient
import okhttp3.Request

enum class IpFamily { Ipv4, Ipv6 }

interface PublicIpProbe {
    suspend fun probe(network: Network): PublicIpSnapshot
}

data class CellularHttpBinding(
    val socketFactory: SocketFactory,
    val dns: Dns,
)

fun buildPublicIpHttpClient(
    binding: CellularHttpBinding,
    requestTimeoutMillis: Long,
): OkHttpClient = OkHttpClient.Builder()
    .socketFactory(binding.socketFactory)
    .dns(binding.dns)
    .connectTimeout(requestTimeoutMillis, TimeUnit.MILLISECONDS)
    .readTimeout(requestTimeoutMillis, TimeUnit.MILLISECONDS)
    .callTimeout(requestTimeoutMillis, TimeUnit.MILLISECONDS)
    .retryOnConnectionFailure(false)
    .build()

class IpifyPublicIpProbe(
    private val requestTimeoutMillis: Long = 8_000,
) : PublicIpProbe {
    override suspend fun probe(network: Network): PublicIpSnapshot {
        val client = buildPublicIpHttpClient(
            CellularHttpBinding(
                socketFactory = network.socketFactory,
                dns = object : Dns {
                    override fun lookup(hostname: String): List<InetAddress> =
                        network.getAllByName(hostname).toList()
                },
            ),
            requestTimeoutMillis,
        )
        return try {
            collectPublicIps { family ->
                fetch(client, if (family == IpFamily.Ipv4) IPV4_ENDPOINT else IPV6_ENDPOINT)
            }
        } finally {
            client.connectionPool.evictAll()
            client.dispatcher.executorService.shutdown()
        }
    }

    private suspend fun fetch(client: OkHttpClient, endpoint: String): String = withContext(Dispatchers.IO) {
        client.newCall(Request.Builder().url(endpoint).get().build()).execute().use { response ->
            check(response.isSuccessful) { "Public IP service returned HTTP ${response.code}" }
            val value = checkNotNull(response.body).string()
            check(value.length <= MAX_RESPONSE_LENGTH) { "Public IP response was too large" }
            value
        }
    }

    private companion object {
        const val IPV4_ENDPOINT = "https://api.ipify.org"
        const val IPV6_ENDPOINT = "https://api6.ipify.org"
        const val MAX_RESPONSE_LENGTH = 128
    }
}

suspend fun collectPublicIps(fetch: suspend (IpFamily) -> String): PublicIpSnapshot = coroutineScope {
    val ipv4 = async { safeAddress(IpFamily.Ipv4, fetch) }
    val ipv6 = async { safeAddress(IpFamily.Ipv6, fetch) }
    PublicIpSnapshot(ipv4 = ipv4.await(), ipv6 = ipv6.await())
}

private suspend fun safeAddress(
    family: IpFamily,
    fetch: suspend (IpFamily) -> String,
): String? = try {
    normalizeAddress(family, fetch(family))
} catch (cancelled: CancellationException) {
    throw cancelled
} catch (_: Exception) {
    null
}

private fun normalizeAddress(family: IpFamily, raw: String): String? {
    val candidate = raw.trim()
    if (candidate.isEmpty()) return null
    return when (family) {
        IpFamily.Ipv4 -> candidate.takeIf(::isIpv4Literal)
        IpFamily.Ipv6 -> candidate.takeIf(::isIpv6Literal)?.lowercase()
    }
}

private fun isIpv4Literal(value: String): Boolean {
    val parts = value.split('.')
    return parts.size == 4 && parts.all { part ->
        part.isNotEmpty() &&
            part.length <= 3 &&
            part.all(Char::isDigit) &&
            part.toIntOrNull() in 0..255
    }
}

private fun isIpv6Literal(value: String): Boolean =
    value.contains(':') && runCatching { InetAddress.getByName(value) is Inet6Address }.getOrDefault(false)
