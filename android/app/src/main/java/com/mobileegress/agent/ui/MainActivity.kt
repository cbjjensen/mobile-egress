package com.mobileegress.agent.ui

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.pm.PackageManager
import android.graphics.Color
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import com.mobileegress.agent.network.RotationState
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle

class MainActivity : ComponentActivity() {
    private val viewModel: MainViewModel by viewModels()
    private val notificationPermission = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) {
        viewModel.startAgent()
    }
    private val cameraPermission = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        viewModel.onCameraPermissionResult(granted)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        setContent {
            val state by viewModel.state.collectAsStateWithLifecycle()
            val rotation = state.runtime.rotation
            LaunchedEffect(rotation) {
                val attemptId = (rotation as? RotationState.AwaitingAirplaneOn)?.attemptId
                if (attemptId != null && viewModel.consumeAirplaneSettingsLaunch(attemptId)) {
                    openAirplaneModeSettings()
                }
            }
            MobileEgressTheme {
                AgentScreen(
                    state = state,
                    onScanQr = ::scanQrFromVisibleUi,
                    onCancelQrScan = viewModel::cancelQrScan,
                    onQrDecoded = viewModel::onQrDecoded,
                    onQrNotRecognized = viewModel::onQrNotRecognized,
                    onScannerUnavailable = viewModel::onScannerUnavailable,
                    onStart = ::startFromVisibleUi,
                    onStop = viewModel::stopAgent,
                    onRotateIp = viewModel::rotateCellularIp,
                    onCancelRotation = viewModel::cancelCellularIpRotation,
                    onCopyStatus = ::copySafeStatus,
                )
            }
        }
    }

    private fun scanQrFromVisibleUi() {
        when (
            viewModel.requestQrScan(
                ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED,
            )
        ) {
            ScanRequest.RequestCameraPermission -> cameraPermission.launch(Manifest.permission.CAMERA)
            ScanRequest.None,
            ScanRequest.StartScanner,
            -> Unit
        }
    }

    private fun startFromVisibleUi() {
        if (
            Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            viewModel.startAgent()
        }
    }

    private fun copySafeStatus() {
        getSystemService(ClipboardManager::class.java).setPrimaryClip(
            ClipData.newPlainText("Mobile Egress status", viewModel.copySafeStatus()),
        )
    }

    private fun openAirplaneModeSettings() {
        val airplaneSettings = Intent(Settings.ACTION_AIRPLANE_MODE_SETTINGS)
        val intent = if (airplaneSettings.resolveActivity(packageManager) != null) {
            airplaneSettings
        } else {
            Intent(Settings.ACTION_WIRELESS_SETTINGS)
        }
        startActivity(intent)
    }
}
