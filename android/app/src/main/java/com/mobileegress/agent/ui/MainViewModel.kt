package com.mobileegress.agent.ui

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.mobileegress.agent.direct.*
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.network.isActive
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.SecureIdentityStore
import com.mobileegress.agent.service.AgentForegroundService
import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.AgentStatusBus
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class ClientUiState(
    val id: String, val name: String, val enabled: Boolean, val stage: String,
    val connected: Boolean, val streams: Int, val error: String = "",
    val removalPending: Boolean = false,
)
data class MainUiState(
    val pairingInProgress: Boolean = false,
    val paired: Boolean = false,
    val pairingStatus: String = "Unpaired",
    val pairingScanState: PairingScanState = PairingScanState.Idle,
    val runtime: AgentRuntimeStatus = AgentRuntimeStatus(),
    val clients: List<ClientUiState> = emptyList(),
)

class MainViewModel(application: Application) : AndroidViewModel(application) {
    private val rotationSettingsLaunchGate = RotationSettingsLaunchGate()
    private val registry = DirectRegistry(SecureIdentityStore(application))
    private val repository = DirectRepository(registry, DeviceKeyStore(), CellularNetworkAcquirer(application))
    private val scan = PairingScanSession()
    private val mutableState = MutableStateFlow(MainUiState())
    private val actionRecoveries = mutableMapOf<String, DirectRecovery>()
    val state: StateFlow<MainUiState> = mutableState.asStateFlow()
    init {
        refresh()
        viewModelScope.launch { AgentStatusBus.status.collect { runtime -> mutableState.update { it.copy(runtime = runtime) } } }
        viewModelScope.launch { DirectRuntimeBus.peers.collect { peers ->
            peers.filter { it.connected }.forEach { peer ->
                val recovery = actionRecoveries[peer.clientId]
                if (recovery?.automaticRetry == true || recovery == DirectRecovery.RetryWhileStopped) actionRecoveries.remove(peer.clientId)
            }
            refresh()
        } }
        viewModelScope.launch { DirectRegistrySignals.revision.collect { refresh() } }
    }
    private fun refresh() {
        try {
            val saved = registry.snapshot()
            actionRecoveries.keys.retainAll(saved.records.map { it.clientId }.toSet())
            val peers = DirectRuntimeBus.peers.value
            mutableState.update { old -> old.copy(
                paired = saved.records.isNotEmpty(),
                pairingStatus = if (saved.migrationRequired && saved.records.isEmpty()) "Upgrade requires pairing each Client again" else old.pairingStatus,
                clients = saved.records.map { record ->
                    val peer = peers.find { it.clientId == record.clientId }
                    val recovery = actionRecoveries[record.clientId] ?: peer?.recovery ?: DirectRecovery.None
                    ClientUiState(record.clientId, record.displayName, record.enabled,
                        when (record.stage) { DirectStage.Pending -> "Pairing pending"; DirectStage.AwaitingAck -> "Acknowledgement pending"; DirectStage.Paired -> "Paired" },
                        peer?.connected == true, peer?.streams ?: 0,
                        recovery.message.ifEmpty { peer?.error?.takeIf { it != com.mobileegress.agent.status.ErrorClass.None }?.name?.replace("Relay", "Client") ?: "" },
                        removalPending = record.removalPending)
                },
            ) }
        } catch (_: Exception) { mutableState.update { old -> old.copy(
            pairingStatus = "Credential storage unavailable",
            clients = old.clients.map { client ->
                if (registry.removals.isBlocked(client.id)) client.copy(enabled = false, connected = false, streams = 0, removalPending = true) else client
            },
        ) } }
    }
    fun requestQrScan(cameraPermissionGranted: Boolean): ScanRequest {
        if (state.value.pairingInProgress) return ScanRequest.None
        return scan.requestScan(cameraPermissionGranted).also { syncScan() }
    }
    fun onCameraPermissionResult(granted: Boolean) = scan.onCameraPermissionResult(granted).also { syncScan() }
    fun cancelQrScan() { if (!state.value.pairingInProgress) { scan.cancel(); syncScan() } }
    fun onQrNotRecognized() { scan.rejectUnrecognizedQr(); syncScan() }
    fun onScannerUnavailable() { scan.onScannerUnavailable(); syncScan() }
    private fun syncScan() { mutableState.update { it.copy(pairingScanState = scan.state) } }
    fun onQrDecoded(scannedBundle: String) {
        if (!scan.acceptDecoded(scannedBundle)) return
        syncScan()
        importBundle(scannedBundle)
    }
    fun importBundle(bundle: String) = operate {
        when (DirectBundles.type(bundle.trim())) {
            "mobile-egress-direct-invitation" -> repository.pair(bundle.trim())
            "mobile-egress-direct-endpoint-update" -> repository.importEndpoint(bundle.trim())
            else -> throw DirectException("legacy_pairing_rejected")
        }
    }
    fun importFile(uri: Uri) = operate {
        val value = withContext(Dispatchers.IO) {
            getApplication<Application>().contentResolver.openInputStream(uri)?.use { input ->
                val buffer = ByteArray(87_385)
                var count = 0
                while (count < buffer.size) {
                    val read = input.read(buffer, count, buffer.size - count)
                    if (read < 0) break
                    count += read
                }
                if (count == buffer.size) throw DirectException("invalid_bundle")
                DirectBundles.text(buffer.copyOf(count)).trim()
            } ?: throw DirectException("invalid_bundle")
        }
        if (DirectBundles.type(value) != "mobile-egress-direct-endpoint-update") throw DirectException("invalid_endpoint_update")
        repository.importEndpoint(value)
    }
    fun retryClient(id: String) = operate(id) { withContext(Dispatchers.IO) { retryDirectClient(id, registry, repository::retry) } }
    fun setClientEnabled(id: String, enabled: Boolean) = operate(id) { withContext(Dispatchers.IO) { registry.setEnabled(id, enabled) } }
    fun removeClient(id: String) = operate(id) { withContext(Dispatchers.IO) { repository.remove(id) } }
    private fun operate(clientId: String? = null, action: suspend () -> Any?) {
        if (state.value.pairingInProgress) return
        mutableState.update { it.copy(pairingInProgress = true, pairingStatus = "Working…") }
        viewModelScope.launch {
            var message = "Client updated"
            try {
                val updated = action()
                (clientId ?: (updated as? DirectRecord)?.clientId)?.let { actionRecoveries.remove(it) }
            }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) {
                clientId?.let { actionRecoveries[it] = directRecovery(error) }
                message = when ((error as? DirectException)?.code) {
                "legacy_pairing_rejected" -> "Old relay QR is unsupported. Generate a direct Client invitation."
                "ten_client_limit" -> "Ten Clients saved. Remove a Client before adding another."
                "client_already_paired" -> "Client already saved. Use Retry or remove it before pairing again."
                "invalid_or_expired_invitation" -> "Invitation invalid or expired"
                "client_connection_failed" -> "Client unavailable. Check cellular and endpoint reachability, then Retry."
                "client_authorization_rejected" -> "Client authorization rejected. Revoke and pair again on the workload."
                "removal_pending" -> "Client stopped. Retry Remove, or choose Enable to trust it again."
                "removal_stop_not_saved" -> "Client stopped for this session, but the stop could not be saved. Retry Remove before restarting the app."
                "client_repair_required", "client_certificate_expired", "client_trust_invalid", "key_unavailable", "identity_unavailable",
                "credential_storage_unavailable", "client_clock_invalid", "client_storage_unavailable" -> directRecovery(error).message
                else -> if (error is com.mobileegress.agent.security.CredentialStoreException) DirectRecovery.UnlockStorage.message
                    else "Client operation failed. Check the invitation or connection update and retry."
            } }
            finally {
                scan.cancel()
                mutableState.update { it.copy(pairingInProgress = false, pairingStatus = message, pairingScanState = scan.state) }
                refresh()
            }
        }
    }
    fun startAgent() { if (state.value.paired && !state.value.runtime.running) AgentForegroundService.startFromUi(getApplication()) }
    fun stopAgent() { if (state.value.runtime.running) AgentForegroundService.stopFromUi(getApplication()) }
    fun rotateCellularIp(holdSeconds: Int) {
        if (state.value.runtime.running && !state.value.runtime.rotation.isActive()) AgentForegroundService.rotateIpFromUi(getApplication(), holdSeconds)
    }
    fun cancelCellularIpRotation() { if (state.value.runtime.rotation.isActive()) AgentForegroundService.cancelRotationFromUi(getApplication()) }
    fun consumeAirplaneSettingsLaunch(attemptId: Long) = rotationSettingsLaunchGate.consume(attemptId)
    fun copySafeStatus(): String = state.value.runtime.copySafeText(state.value.paired) +
        "\nSaved Clients: " + state.value.clients.size + "\nConnected Clients: " + state.value.clients.count { it.connected }
}
