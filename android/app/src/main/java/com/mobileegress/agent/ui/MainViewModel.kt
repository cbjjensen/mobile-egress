package com.mobileegress.agent.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.network.CellularUnavailable
import com.mobileegress.agent.pairing.EnrollmentClient
import com.mobileegress.agent.pairing.EnrollmentException
import com.mobileegress.agent.pairing.EnrollmentRepository
import com.mobileegress.agent.pairing.PairingBundleException
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.CredentialStoreException
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.SecureIdentityStore
import com.mobileegress.agent.service.AgentForegroundService
import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.AgentStatusBus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class MainUiState(
    val pairingBundle: String = "",
    val pairingInProgress: Boolean = false,
    val paired: Boolean = false,
    val pairingStatus: String = "Unpaired",
    val runtime: AgentRuntimeStatus = AgentRuntimeStatus(),
)

class MainViewModel(application: Application) : AndroidViewModel(application) {
    private val identityStore = SecureIdentityStore(application)
    private val enrollmentRepository = EnrollmentRepository(
        parser = PairingBundleParser(),
        networkAcquirer = CellularNetworkAcquirer(application),
        deviceKeyStore = DeviceKeyStore(),
        identityStore = identityStore,
        enrollmentClient = EnrollmentClient(),
    )
    private val mutableState = MutableStateFlow(initialState())
    val state: StateFlow<MainUiState> = mutableState.asStateFlow()

    init {
        viewModelScope.launch {
            AgentStatusBus.status.collect { runtime ->
                mutableState.update { it.copy(runtime = runtime) }
            }
        }
    }

    fun updatePairingBundle(value: String) {
        mutableState.update { it.copy(pairingBundle = value, pairingStatus = if (it.paired) "Paired" else "Unpaired") }
    }

    fun pair() {
        val immutableBundle = mutableState.value.pairingBundle
        if (immutableBundle.isBlank() || mutableState.value.pairingInProgress) return
        mutableState.update {
            it.copy(pairingBundle = "", pairingInProgress = true, pairingStatus = "Pairing")
        }
        viewModelScope.launch {
            val result = runCatching { enrollmentRepository.pair(immutableBundle) }
            val error = result.exceptionOrNull()
            mutableState.update {
                if (result.isSuccess) {
                    it.copy(pairingInProgress = false, paired = true, pairingStatus = "Paired")
                } else {
                    it.copy(
                        pairingInProgress = false,
                        pairingStatus = when (error) {
                            is PairingBundleException -> "Pairing bundle rejected"
                            is CellularUnavailable -> "Cellular unavailable"
                            is EnrollmentException -> "Relay enrollment rejected"
                            is CredentialStoreException -> "Credential storage unavailable"
                            else -> "Pairing failed"
                        },
                    )
                }
            }
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

    fun copySafeStatus(): String = mutableState.value.runtime.copySafeText(mutableState.value.paired)

    private fun initialState(): MainUiState = try {
        val paired = identityStore.load() != null
        MainUiState(paired = paired, pairingStatus = if (paired) "Paired" else "Unpaired")
    } catch (_: CredentialStoreException) {
        MainUiState(pairingStatus = "Credential storage unavailable")
    }
}
