package com.mobileegress.agent.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import com.mobileegress.agent.R
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.network.CellularRequiredController
import com.mobileegress.agent.network.NetworkTransport
import com.mobileegress.agent.network.PathEvent
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.SecureIdentityStore
import com.mobileegress.agent.session.AgentSession
import com.mobileegress.agent.session.AgentSessionListener
import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.AgentStatusBus
import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import com.mobileegress.agent.ui.MainActivity
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

class AgentForegroundService : LifecycleService() {
    private val foregroundController = ForegroundController()
    private val pathController = CellularRequiredController()
    private val runtimeLock = Any()
    private lateinit var connectivityManager: ConnectivityManager
    private lateinit var identityStore: SecureIdentityStore
    private val deviceKeyStore = DeviceKeyStore()
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private var selectedNetwork: Network? = null
    private var selectedToken: String? = null
    private var session: AgentSession? = null
    private var reconnectJob: Job? = null
    private var generation = 0L
    private var reconnectAttempt = 0

    override fun onCreate() {
        super.onCreate()
        connectivityManager = getSystemService(ConnectivityManager::class.java)
        identityStore = SecureIdentityStore(this)
        foregroundController.reduce(ForegroundEvent.ServiceCreated)
        lifecycleScope.launch {
            AgentStatusBus.status.collectLatest { status ->
                if (foregroundController.state != ForegroundState.Stopped) {
                    getSystemService(NotificationManager::class.java).notify(
                        NOTIFICATION_ID,
                        notification(status),
                    )
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        when (intent?.action) {
            ACTION_START_FROM_UI -> execute(foregroundController.reduce(ForegroundEvent.UiStartRequested).effects)
            ACTION_STOP_FROM_UI -> execute(foregroundController.reduce(ForegroundEvent.UiStopRequested).effects)
            else -> if (foregroundController.state == ForegroundState.Stopped) stopSelf(startId)
        }
        return Service.START_NOT_STICKY
    }

    override fun onDestroy() {
        stopCellularRuntime()
        super.onDestroy()
    }

    private fun execute(effects: List<ForegroundEffect>) {
        effects.forEach { effect ->
            when (effect) {
                ForegroundEffect.CreateNotificationChannel -> createNotificationChannel()
                ForegroundEffect.EnterForeground -> enterForeground()
                ForegroundEffect.StartCellularRuntime -> startCellularRuntime()
                ForegroundEffect.StopCellularRuntime -> {
                    stopCellularRuntime()
                    execute(foregroundController.reduce(ForegroundEvent.RuntimeStopped).effects)
                }
                ForegroundEffect.ExitForegroundAndStopService -> {
                    ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
            }
        }
    }

    private fun enterForeground() {
        ServiceCompat.startForeground(
            this,
            NOTIFICATION_ID,
            notification(AgentStatusBus.status.value.copy(running = true)),
            if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0,
        )
    }

    private fun startCellularRuntime() {
        val hasIdentity = try {
            identityStore.load() != null
        } catch (_: Exception) {
            false
        }
        if (!hasIdentity) {
            AgentStatusBus.update { it.copy(running = true, errorClass = ErrorClass.Credential) }
            execute(foregroundController.reduce(ForegroundEvent.UiStopRequested).effects)
            return
        }
        pathController.reduce(PathEvent.StartRequested)
        AgentStatusBus.update {
            it.copy(
                running = true,
                cellular = CellularHealth.Unavailable,
                relay = RelayHealth.Disconnected,
                activeStreams = 0,
                errorClass = ErrorClass.None,
            )
        }
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                val token: String
                synchronized(runtimeLock) {
                    if (networkCallback !== this || selectedNetwork != null) return
                    selectedNetwork = network
                    token = (++generation).toString()
                    selectedToken = token
                }
                val transition = pathController.reduce(
                    PathEvent.NetworkAvailable(token, NetworkTransport.CELLULAR),
                )
                if (transition.effects.isNotEmpty()) {
                    AgentStatusBus.update {
                        it.copy(
                            cellular = CellularHealth.Available,
                            relay = RelayHealth.Connecting,
                            errorClass = ErrorClass.None,
                        )
                    }
                    connectRelay(network, token)
                }
            }

            override fun onLost(network: Network) {
                val token: String
                val oldSession: AgentSession?
                synchronized(runtimeLock) {
                    if (selectedNetwork != network) return
                    token = selectedToken ?: return
                    selectedNetwork = null
                    selectedToken = null
                    generation++
                    reconnectJob?.cancel()
                    reconnectJob = null
                    oldSession = session
                    session = null
                }
                pathController.reduce(PathEvent.NetworkLost(token))
                oldSession?.close()
                reconnectAttempt = 0
                AgentStatusBus.update {
                    it.copy(
                        cellular = CellularHealth.Unavailable,
                        relay = RelayHealth.Disconnected,
                        activeStreams = 0,
                        errorClass = ErrorClass.CellularUnavailable,
                    )
                }
            }

            override fun onUnavailable() {
                AgentStatusBus.update {
                    it.copy(
                        cellular = CellularHealth.Unavailable,
                        relay = RelayHealth.Disconnected,
                        errorClass = ErrorClass.CellularUnavailable,
                    )
                }
            }
        }
        networkCallback = callback
        connectivityManager.requestNetwork(CellularNetworkAcquirer.cellularRequest(), callback)
        foregroundController.reduce(ForegroundEvent.RuntimeStarted)
    }

    private fun connectRelay(network: Network, token: String) {
        val attemptGeneration = synchronized(runtimeLock) { generation }
        val identity = try {
            identityStore.load()
        } catch (_: Exception) {
            AgentStatusBus.update { it.copy(errorClass = ErrorClass.Credential) }
            return
        } ?: return
        val newSession = try {
            AgentSession(
                network = network,
                identity = identity,
                deviceKeyStore = deviceKeyStore,
                parentScope = lifecycleScope,
                listener = object : AgentSessionListener {
                    override fun onConnected() {
                        synchronized(runtimeLock) {
                            if (generation != attemptGeneration || session == null) return
                            reconnectAttempt = 0
                        }
                        pathController.reduce(PathEvent.RelayConnected(token))
                        AgentStatusBus.update {
                            it.copy(relay = RelayHealth.Connected, errorClass = ErrorClass.None)
                        }
                    }

                    override fun onTerminated(errorClass: ErrorClass) {
                        val shouldReconnect = synchronized(runtimeLock) {
                            if (generation != attemptGeneration || selectedNetwork != network) return
                            session = null
                            true
                        }
                        if (shouldReconnect) {
                            pathController.reduce(PathEvent.RelayDisconnected(token))
                            AgentStatusBus.update {
                                it.copy(
                                    relay = RelayHealth.Connecting,
                                    activeStreams = 0,
                                    errorClass = errorClass,
                                )
                            }
                            scheduleReconnect(network, token, attemptGeneration)
                        }
                    }
                },
            )
        } catch (_: Exception) {
            AgentStatusBus.update { it.copy(relay = RelayHealth.Disconnected, errorClass = ErrorClass.Credential) }
            return
        }
        synchronized(runtimeLock) {
            if (generation != attemptGeneration || selectedNetwork != network) {
                newSession.close()
                return
            }
            session?.close()
            session = newSession
        }
        newSession.connect()
    }

    private fun scheduleReconnect(network: Network, token: String, expectedGeneration: Long) {
        synchronized(runtimeLock) {
            reconnectJob?.cancel()
            val delayMillis = (2_000L shl reconnectAttempt.coerceAtMost(4)).coerceAtMost(30_000L)
            reconnectAttempt++
            reconnectJob = lifecycleScope.launch {
                delay(delayMillis)
                val allowed = synchronized(runtimeLock) {
                    generation == expectedGeneration && selectedNetwork == network && session == null
                }
                if (allowed) connectRelay(network, token)
            }
        }
    }

    private fun stopCellularRuntime() {
        val callback: ConnectivityManager.NetworkCallback?
        val oldSession: AgentSession?
        synchronized(runtimeLock) {
            generation++
            callback = networkCallback
            networkCallback = null
            selectedNetwork = null
            selectedToken = null
            reconnectJob?.cancel()
            reconnectJob = null
            oldSession = session
            session = null
        }
        if (callback != null) {
            try {
                connectivityManager.unregisterNetworkCallback(callback)
            } catch (_: IllegalArgumentException) {
                // Callback was already released by the platform.
            }
        }
        oldSession?.close()
        pathController.reduce(PathEvent.StopRequested)
        reconnectAttempt = 0
        AgentStatusBus.reset()
    }

    private fun createNotificationChannel() {
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(
                NOTIFICATION_CHANNEL,
                getString(R.string.notification_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = getString(R.string.notification_channel_description)
                setShowBadge(false)
            },
        )
    }

    private fun notification(status: AgentRuntimeStatus): Notification {
        val contentIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, AgentForegroundService::class.java).setAction(ACTION_STOP_FROM_UI),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val summary = "Cellular ${status.cellular.name.lowercase()} · Relay ${status.relay.name.lowercase()} · ${status.activeStreams} streams"
        return NotificationCompat.Builder(this, NOTIFICATION_CHANNEL)
            .setSmallIcon(R.drawable.ic_mobile_egress)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(summary)
            .setContentIntent(contentIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .addAction(0, getString(R.string.stop), stopIntent)
            .build()
    }

    companion object {
        private const val ACTION_START_FROM_UI = "com.mobileegress.agent.action.START_FROM_UI"
        private const val ACTION_STOP_FROM_UI = "com.mobileegress.agent.action.STOP_FROM_UI"
        private const val NOTIFICATION_CHANNEL = "mobile_egress_agent"
        private const val NOTIFICATION_ID = 4101

        fun startFromUi(context: Context) {
            androidx.core.content.ContextCompat.startForegroundService(
                context,
                Intent(context, AgentForegroundService::class.java).setAction(ACTION_START_FROM_UI),
            )
        }

        fun stopFromUi(context: Context) {
            context.startService(
                Intent(context, AgentForegroundService::class.java).setAction(ACTION_STOP_FROM_UI),
            )
        }
    }
}
