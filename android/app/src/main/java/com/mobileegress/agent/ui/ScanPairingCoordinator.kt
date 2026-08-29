package com.mobileegress.agent.ui

import com.mobileegress.agent.pairing.EnrollmentRepository
import com.mobileegress.agent.pairing.PairingController
import com.mobileegress.agent.pairing.PairingEvent
import com.mobileegress.agent.pairing.PairingState

data class PairingUiState(
    val pairingInProgress: Boolean = false,
    val paired: Boolean = false,
    val pairingStatus: String = "Unpaired",
    val pairingScanState: PairingScanState = PairingScanState.Idle,
)

class ScanPairingCoordinator(
    private val enrollmentRepository: EnrollmentRepository,
    initiallyPaired: Boolean,
    initialStatus: String = if (initiallyPaired) "Paired" else "Unpaired",
) {
    private val pairingController = PairingController(initiallyPaired)
    private val pairingScanSession = PairingScanSession()
    private var acceptedScannedBundle: String? = null

    var state = PairingUiState(paired = initiallyPaired, pairingStatus = initialStatus)
        private set

    fun requestScan(cameraPermissionGranted: Boolean): ScanRequest {
        val request = pairingScanSession.requestScan(cameraPermissionGranted)
        syncScanState()
        return request
    }

    fun onCameraPermissionResult(granted: Boolean): ScanRequest {
        val request = pairingScanSession.onCameraPermissionResult(granted)
        syncScanState()
        return request
    }

    fun cancelScan() {
        if (state.pairingInProgress && acceptedScannedBundle == null) {
            return
        }
        val pairingState = if (state.pairingInProgress) {
            pairingController.reduce(PairingEvent.PairFailed)
        } else {
            pairingController.state
        }
        acceptedScannedBundle = null
        pairingScanSession.cancel()
        state = state.copy(
            pairingInProgress = false,
            paired = pairingState == PairingState.Paired,
            pairingScanState = pairingScanSession.state,
            pairingStatus = pairedStatus(),
        )
    }

    fun onQrNotRecognized() {
        pairingScanSession.rejectUnrecognizedQr()
        syncScanState()
    }

    fun onScannerUnavailable() {
        pairingScanSession.onScannerUnavailable()
        syncScanState()
    }

    fun submitDecoded(scannedBundle: String): Boolean {
        if (state.pairingInProgress || !pairingScanSession.acceptDecoded(scannedBundle)) {
            syncScanState()
            return false
        }
        acceptedScannedBundle = scannedBundle
        pairingController.reduce(PairingEvent.PairRequested)
        state = state.copy(
            pairingInProgress = true,
            pairingStatus = "Pairing",
            pairingScanState = pairingScanSession.state,
        )
        return true
    }

    suspend fun enrollAcceptedScan() {
        if (!state.pairingInProgress || state.pairingScanState != PairingScanState.Pairing) return
        val scannedBundle = acceptedScannedBundle ?: return
        acceptedScannedBundle = null
        val result = runCatching { enrollmentRepository.pair(scannedBundle) }
        val pairingState = pairingController.reduce(
            if (result.isSuccess) PairingEvent.PairSucceeded else PairingEvent.PairFailed,
        )
        state = if (result.isSuccess) {
            state.copy(
                pairingInProgress = false,
                paired = pairingState == PairingState.Paired,
                pairingStatus = "Paired",
            )
        } else {
            state.copy(
                pairingInProgress = false,
                paired = pairingState == PairingState.Paired,
                pairingStatus = PairingFailureStatus.forError(requireNotNull(result.exceptionOrNull())),
            )
        }
    }

    private fun syncScanState() {
        state = state.copy(
            pairingScanState = pairingScanSession.state,
            pairingStatus = when (pairingScanSession.state) {
                PairingScanState.CameraPermissionRequired,
                PairingScanState.ScannerUnavailable,
                PairingScanState.QrNotRecognized,
                -> pairingScanSession.status
                else -> state.pairingStatus
            },
        )
    }

    private fun pairedStatus(): String =
        if (pairingController.state == PairingState.Paired) "Paired" else "Unpaired"
}
