package com.mobileegress.agent.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.migration.CellularEndpointMigrationPerformer
import com.mobileegress.agent.migration.EndpointMigrationClient
import com.mobileegress.agent.migration.EndpointMigrationParser
import com.mobileegress.agent.migration.EndpointMigrationRepository
import com.mobileegress.agent.pairing.EnrollmentClient
import com.mobileegress.agent.pairing.CellularEnrollmentPerformer
import com.mobileegress.agent.pairing.EnrollmentRepository
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.CredentialStoreException
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.SecureIdentityStore
import com.mobileegress.agent.service.AgentForegroundService
import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.AgentStatusBus
import com.mobileegress.agent.network.isActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class MainUiState(
    val pairingInProgress: Boolean = false,
    val paired: Boolean = false,
    val pairingStatus: String = "Unpaired",
    val pairingScanState: PairingScanState = PairingScanState.Idle,
    val runtime: AgentRuntimeStatus = AgentRuntimeStatus(),
)

class MainViewModel(application: Application) : AndroidViewModel(application) {
    private val rotationSettingsLaunchGate = RotationSettingsLaunchGate()
    private val identityStore = SecureIdentityStore(application)
    private val deviceKeyStore = DeviceKeyStore()
    private val cellularNetworkAcquirer = CellularNetworkAcquirer(application)
    private val enrollmentRepository = EnrollmentRepository(
        decoder = PairingBundleParser(),
        credentialKeys = deviceKeyStore,
        identityPersistence = identityStore,
        enrollmentPerformer = CellularEnrollmentPerformer(
            cellularNetworkAcquirer,
            EnrollmentClient(),
        ),
    )
    private val migrationParser = EndpointMigrationParser()
    private val migrationRepository = EndpointMigrationRepository(
        decoder = migrationParser,
        identityPersistence = identityStore,
        performer = CellularEndpointMigrationPerformer(
            cellularNetworkAcquirer,
            deviceKeyStore,
            EndpointMigrationClient(),
        ),
    )
    private val initialUiState = initialState()
    private val pairingCoordinator = ScanPairingCoordinator(
        enrollmentRepository = enrollmentRepository,
        initiallyPaired = initialUiState.paired,
        initialStatus = initialUiState.pairingStatus,
    )
    private val mutableState = MutableStateFlow(initialUiState)
    val state: StateFlow<MainUiState> = mutableState.asStateFlow()

    init {
        viewModelScope.launch {
            AgentStatusBus.status.collect { runtime ->
                mutableState.update { it.copy(runtime = runtime) }
            }
        }
    }

    fun requestQrScan(cameraPermissionGranted: Boolean): ScanRequest {
        val request = pairingCoordinator.requestScan(cameraPermissionGranted)
        syncPairingState()
        return request
    }

    fun onCameraPermissionResult(granted: Boolean): ScanRequest {
        val request = pairingCoordinator.onCameraPermissionResult(granted)
        syncPairingState()
        return request
    }

    fun cancelQrScan() {
        pairingCoordinator.cancelScan()
        syncPairingState()
    }

    fun onQrNotRecognized() {
        pairingCoordinator.onQrNotRecognized()
        syncPairingState()
    }

    fun onScannerUnavailable() {
        pairingCoordinator.onScannerUnavailable()
        syncPairingState()
    }

    fun onQrDecoded(scannedBundle: String) {
        if (!pairingCoordinator.submitDecoded(scannedBundle)) {
            syncPairingState()
            return
        }
        syncPairingState()
        viewModelScope.launch {
            if (migrationParser.recognizes(scannedBundle) && mutableState.value.paired) {
                pairingCoordinator.migrateAcceptedScan { migrationRepository.migrate(it) }
            } else {
                pairingCoordinator.enrollAcceptedScan()
            }
            syncPairingState()
        }
    }

    fun startAgent() {
        if (mutableState.value.paired && !mutableState.value.runtime.running) {
            AgentForegroundService.startFromUi(getApplication())
        }
    }

    fun stopAgent() {
        if (mutableState.value.runtime.running) {
            AgentForegroundService.stopFromUi(getApplication())
        }
    }

    fun rotateCellularIp(holdSeconds: Int) {
        val current = mutableState.value
        if (
            current.paired &&
            current.runtime.running &&
            !current.runtime.rotation.isActive()
        ) {
            AgentForegroundService.rotateIpFromUi(getApplication(), holdSeconds)
        }
    }

    fun cancelCellularIpRotation() {
        if (mutableState.value.runtime.rotation.isActive()) {
            AgentForegroundService.cancelRotationFromUi(getApplication())
        }
    }

    fun consumeAirplaneSettingsLaunch(attemptId: Long): Boolean =
        rotationSettingsLaunchGate.consume(attemptId)

    fun copySafeStatus(): String = mutableState.value.runtime.copySafeText(mutableState.value.paired)

    private fun syncPairingState() {
        val pairing = pairingCoordinator.state
        mutableState.update {
            it.copy(
                pairingInProgress = pairing.pairingInProgress,
                paired = pairing.paired,
                pairingStatus = pairing.pairingStatus,
                pairingScanState = pairing.pairingScanState,
            )
        }
    }

    private fun initialState(): MainUiState = try {
        val paired = identityStore.load() != null
        MainUiState(paired = paired, pairingStatus = if (paired) "Paired" else "Unpaired")
    } catch (_: CredentialStoreException) {
        MainUiState(pairingStatus = "Credential storage unavailable")
    }
}
