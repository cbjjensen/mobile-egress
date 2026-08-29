package com.mobileegress.agent.ui

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle

class MainActivity : ComponentActivity() {
    private val viewModel: MainViewModel by viewModels()
    private val notificationPermission = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) {
        viewModel.startAgent()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            val state by viewModel.state.collectAsStateWithLifecycle()
            MobileEgressTheme {
                AgentScreen(
                    state = state,
                    onBundleChanged = viewModel::updatePairingBundle,
                    onPair = viewModel::pair,
                    onStart = ::startFromVisibleUi,
                    onStop = viewModel::stopAgent,
                    onCopyStatus = ::copySafeStatus,
                )
            }
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
}

@Composable
private fun AgentScreen(
    state: MainUiState,
    onBundleChanged: (String) -> Unit,
    onPair: () -> Unit,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onCopyStatus: () -> Unit,
) {
    Surface(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("Mobile Egress", style = MaterialTheme.typography.headlineMedium)
            Text(
                "Owner-paired cellular Agent",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Card(modifier = Modifier.fillMaxWidth()) {
                Column(
                    modifier = Modifier.padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text("Pair", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "Paste the immutable Agent bundle supplied by the enrolled owner. Its contents stay masked and are cleared before enrollment.",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    OutlinedTextField(
                        value = state.pairingBundle,
                        onValueChange = onBundleChanged,
                        modifier = Modifier.fillMaxWidth(),
                        label = { Text("Owner pairing bundle") },
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                        singleLine = true,
                        enabled = !state.pairingInProgress && !state.runtime.running,
                    )
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Button(
                            onClick = onPair,
                            enabled = state.pairingBundle.isNotBlank() && !state.pairingInProgress && !state.runtime.running,
                        ) {
                            Text(if (state.pairingInProgress) "Pairing" else "Pair")
                        }
                        Text(state.pairingStatus, modifier = Modifier.padding(top = 12.dp))
                    }
                }
            }

            Card(modifier = Modifier.fillMaxWidth()) {
                Column(
                    modifier = Modifier.padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text("Agent", style = MaterialTheme.typography.titleMedium)
                    StatusRow("Cellular", state.runtime.cellular.name)
                    StatusRow("Relay", state.runtime.relay.name)
                    StatusRow("Active streams", state.runtime.activeStreams.toString())
                    StatusRow("Bytes up", state.runtime.bytesUp.toString())
                    StatusRow("Bytes down", state.runtime.bytesDown.toString())
                    StatusRow("Error class", state.runtime.errorClass.name)
                    Spacer(modifier = Modifier.height(4.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Button(
                            onClick = onStart,
                            enabled = state.paired && !state.runtime.running,
                        ) { Text("Start") }
                        Button(
                            onClick = onStop,
                            enabled = state.runtime.running,
                        ) { Text("Stop") }
                        Button(onClick = onCopyStatus) { Text("Copy status") }
                    }
                }
            }
        }
    }
}

@Composable
private fun StatusRow(label: String, value: String) {
    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value.lowercase())
    }
}

@Composable
private fun MobileEgressTheme(content: @Composable () -> Unit) {
    MaterialTheme(content = content)
}
