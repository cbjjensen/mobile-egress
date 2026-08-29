package com.mobileegress.agent.network

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import java.io.Closeable
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.withTimeout

class CellularUnavailable : Exception("Cellular network is unavailable")

class CellularNetworkLease internal constructor(
    val network: Network,
    private val connectivityManager: ConnectivityManager,
    private val callback: ConnectivityManager.NetworkCallback,
) : Closeable {
    override fun close() {
        try {
            connectivityManager.unregisterNetworkCallback(callback)
        } catch (_: IllegalArgumentException) {
            // Already unregistered by cancellation or teardown.
        }
    }
}

class CellularNetworkAcquirer(context: Context) {
    private val connectivityManager = context.getSystemService(ConnectivityManager::class.java)

    suspend fun acquire(timeoutMillis: Long = 30_000): CellularNetworkLease =
        try {
            withTimeout(timeoutMillis) {
                suspendCancellableCoroutine { continuation ->
                    lateinit var callback: ConnectivityManager.NetworkCallback
                    callback = object : ConnectivityManager.NetworkCallback() {
                        override fun onAvailable(network: Network) {
                            if (continuation.isActive) {
                                continuation.resume(CellularNetworkLease(network, connectivityManager, this))
                            }
                        }

                        override fun onUnavailable() {
                            if (continuation.isActive) continuation.resumeWithException(CellularUnavailable())
                        }
                    }
                    continuation.invokeOnCancellation {
                        try {
                            connectivityManager.unregisterNetworkCallback(callback)
                        } catch (_: IllegalArgumentException) {
                            // Callback can race with cancellation.
                        }
                    }
                    connectivityManager.requestNetwork(cellularRequest(), callback, timeoutMillis.toInt())
                }
            }
        } catch (_: TimeoutCancellationException) {
            throw CellularUnavailable()
        }

    companion object {
        fun cellularRequest(): NetworkRequest = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_CELLULAR)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
    }
}
