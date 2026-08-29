package com.mobileegress.agent.ui

import com.mobileegress.agent.network.CellularUnavailable
import com.mobileegress.agent.pairing.EnrollmentException
import com.mobileegress.agent.pairing.PairingBundleException
import com.mobileegress.agent.security.CredentialStoreException

enum class PairingScanState {
    Idle,
    AwaitingCameraPermission,
    Scanning,
    CameraPermissionRequired,
    ScannerUnavailable,
    QrNotRecognized,
    Pairing,
}

enum class ScanRequest {
    None,
    RequestCameraPermission,
    StartScanner,
}

class PairingScanSession {
    var state: PairingScanState = PairingScanState.Idle
        private set

    val status: String
        get() = when (state) {
            PairingScanState.CameraPermissionRequired -> "Camera permission required"
            PairingScanState.ScannerUnavailable -> "Scanner unavailable"
            PairingScanState.QrNotRecognized -> "QR not recognized"
            else -> "Unpaired"
        }

    fun requestScan(cameraPermissionGranted: Boolean): ScanRequest {
        return if (cameraPermissionGranted) {
            state = PairingScanState.Scanning
            ScanRequest.StartScanner
        } else {
            state = PairingScanState.AwaitingCameraPermission
            ScanRequest.RequestCameraPermission
        }
    }

    fun onCameraPermissionResult(granted: Boolean): ScanRequest {
        if (state != PairingScanState.AwaitingCameraPermission) return ScanRequest.None
        return if (granted) {
            state = PairingScanState.Scanning
            ScanRequest.StartScanner
        } else {
            state = PairingScanState.CameraPermissionRequired
            ScanRequest.None
        }
    }

    fun acceptDecoded(decodedValue: String): Boolean {
        if (state != PairingScanState.Scanning) return false
        if (decodedValue.isEmpty()) {
            state = PairingScanState.QrNotRecognized
            return false
        }
        state = PairingScanState.Pairing
        return true
    }

    fun rejectUnrecognizedQr() {
        if (state == PairingScanState.Scanning) state = PairingScanState.QrNotRecognized
    }

    fun onScannerUnavailable() {
        if (state == PairingScanState.Scanning) state = PairingScanState.ScannerUnavailable
    }

    fun cancel() {
        state = PairingScanState.Idle
    }
}

object PairingFailureStatus {
    fun forError(error: Throwable): String = when (error) {
        is PairingBundleException -> "Pairing bundle rejected"
        is CellularUnavailable -> "Cellular unavailable"
        is EnrollmentException -> "Relay enrollment rejected"
        is CredentialStoreException -> "Credential storage unavailable"
        else -> "Pairing failed"
    }
}
