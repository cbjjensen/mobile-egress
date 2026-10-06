package com.mobileegress.agent.ui

import android.content.res.Configuration
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.mobileegress.agent.R
import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import com.mobileegress.agent.network.isActive

@Composable
fun AgentScreen(
    state: MainUiState,
    onScanQr: () -> Unit,
    onCancelQrScan: () -> Unit,
    onQrDecoded: (String) -> Unit,
    onQrNotRecognized: () -> Unit,
    onScannerUnavailable: () -> Unit,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRotateIp: (Int) -> Unit,
    onCancelRotation: () -> Unit,
    onCopyStatus: () -> Unit,
    onClientEnabled: (String, Boolean) -> Unit = { _, _ -> },
    onRemoveClient: (String) -> Unit = {},
    onRetryClient: (String) -> Unit = {},
    onImportBundle: (String) -> Unit = {},
    onImportFile: () -> Unit = {},
) {
    val presentation = presentAgentScreen(state)
    val colorScheme = MaterialTheme.colorScheme

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(colorScheme.background),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .windowInsetsPadding(WindowInsets.safeDrawing)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 20.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            AppHeader(presentation)
            PairingCard(
                state = state,
                presentation = presentation,
                onScanQr = onScanQr,
                onCancelQrScan = onCancelQrScan,
                onQrDecoded = onQrDecoded,
                onQrNotRecognized = onQrNotRecognized,
                onScannerUnavailable = onScannerUnavailable,
            )
            DirectClientsCard(state, onClientEnabled, onRemoveClient, onRetryClient, onImportBundle, onImportFile)
            AgentCard(
                state = state,
                presentation = presentation,
                onStart = onStart,
                onStop = onStop,
                onRotateIp = onRotateIp,
                onCancelRotation = onCancelRotation,
                onCopyStatus = onCopyStatus,
            )
            Text(
                text = "Cellular only • No Wi-Fi fallback",
                modifier = Modifier
                    .align(Alignment.CenterHorizontally)
                    .padding(top = 2.dp, bottom = 8.dp),
                color = colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
    }
}

@Composable
private fun DirectClientsCard(
    state: MainUiState, enable: (String, Boolean) -> Unit, remove: (String) -> Unit,
    retry: (String) -> Unit, import: (String) -> Unit, importFile: () -> Unit,
) {
    var text by remember { mutableStateOf("") }
    var removal by remember { mutableStateOf<ClientUiState?>(null) }
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Clients (" + state.clients.size + "/10)", style = MaterialTheme.typography.titleMedium)
            state.clients.forEach { client ->
                Text(client.name, fontWeight = FontWeight.Bold)
                Text(if (client.transport == com.mobileegress.agent.direct.DirectTransport.Hosted) "Inevitable Gateway" else "Direct connection")
                Text(if (client.removalPending) "Stopped · Removal pending" else if (!client.enabled) "Disabled" else if (client.connected) "Connected · " + client.streams + " streams" else client.stage + " · Disconnected")
                if (client.error.isNotEmpty()) Text(client.error, color = MaterialTheme.colorScheme.error)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = { enable(client.id, !client.enabled) }, enabled = !state.pairingInProgress) {
                        Text(if (client.enabled) "Disable" else "Enable")
                    }
                    TextButton(onClick = { retry(client.id) }, enabled = !state.pairingInProgress && !client.removalPending) { Text("Retry") }
                    TextButton(onClick = { removal = client }, enabled = !state.pairingInProgress) { Text(if (client.removalPending) "Retry Remove" else "Remove") }
                }
            }
            OutlinedTextField(value = text, onValueChange = { if (it.length <= 87_384) text = it },
                label = { Text("Invitation or connection update") }, modifier = Modifier.fillMaxWidth(), maxLines = 3)
            Row {
                TextButton(onClick = { val value = text; text = ""; import(value) }, enabled = text.isNotBlank() && !state.pairingInProgress) { Text("Import") }
                TextButton(onClick = importFile, enabled = !state.pairingInProgress) { Text("Import update file") }
            }
        }
    }
    removal?.let { client ->
        AlertDialog(onDismissRequest = { removal = null },
            title = { Text("Remove Client?") },
            text = { Text("This closes its streams and removes this phone's pairing. Revoke the phone on the workload Client before pairing again.") },
            confirmButton = { TextButton(onClick = { removal = null; remove(client.id) }) { Text("Remove") } },
            dismissButton = { TextButton(onClick = { removal = null }) { Text("Cancel") } })
    }
}

@Composable
private fun AppHeader(presentation: AgentScreenPresentation) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Surface(
                modifier = Modifier.size(42.dp),
                shape = RoundedCornerShape(14.dp),
                color = Color.Black,
            ) {
                Image(
                    painter = painterResource(R.drawable.ic_mobile_egress_foreground),
                    contentDescription = null,
                    modifier = Modifier.fillMaxSize(),
                )
            }
            Spacer(modifier = Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = presentation.appTitle,
                    color = MaterialTheme.colorScheme.onBackground,
                    fontWeight = FontWeight.Bold,
                    fontSize = 13.sp,
                    letterSpacing = 1.4.sp,
                )
                Text(
                    text = presentation.appSubtitle,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
        StatusBadge(presentation.badge, presentation.tone)
        Text(
            text = presentation.headline,
            color = MaterialTheme.colorScheme.onBackground,
            style = MaterialTheme.typography.headlineLarge,
        )
        Text(
            text = presentation.summary,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyLarge,
        )
    }
}

@Composable
private fun PairingCard(
    state: MainUiState,
    presentation: AgentScreenPresentation,
    onScanQr: () -> Unit,
    onCancelQrScan: () -> Unit,
    onQrDecoded: (String) -> Unit,
    onQrNotRecognized: () -> Unit,
    onScannerUnavailable: () -> Unit,
) {
    AppCard {
        SectionHeader(
            step = "01",
            label = "PAIRING",
            status = presentation.pairingStatus,
            tone = presentation.pairingTone,
        )
        if (presentation.pairingStatus != state.pairingStatus) {
            Text(
                text = presentation.pairingStatus,
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        Text(
            text = if (state.paired) "Phone paired" else "Link this phone",
            style = MaterialTheme.typography.titleLarge,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Text(
            text = if (state.paired) {
                "Scan a direct Client invitation or a signed connection update. Up to ten Clients can use this phone."
            } else {
                "Generate an invitation on a reachable workload Client, then scan it here to pair this phone."
            },
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Button(
            onClick = onScanQr,
            enabled = presentation.scanEnabled,
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 54.dp),
            shape = RoundedCornerShape(17.dp),
        ) {
            Text(presentation.scanLabel)
        }
        if (state.runtime.running) {
            Text(
                text = "Stop the Agent before scanning another QR.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (state.pairingScanState == PairingScanState.Scanning) {
            QrCodeScanner(
                lifecycleOwner = LocalLifecycleOwner.current,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(300.dp)
                    .clip(RoundedCornerShape(20.dp)),
                onQrDecoded = onQrDecoded,
                onQrNotRecognized = onQrNotRecognized,
                onScannerUnavailable = onScannerUnavailable,
            )
            OutlinedButton(
                onClick = onCancelQrScan,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(17.dp),
            ) {
                Text("Cancel scan")
            }
        }
    }
}

@Composable
private fun AgentCard(
    state: MainUiState,
    presentation: AgentScreenPresentation,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRotateIp: (Int) -> Unit,
    onCancelRotation: () -> Unit,
    onCopyStatus: () -> Unit,
) {
    val runtime = state.runtime
    var pendingRotationSeconds by remember { mutableStateOf<Int?>(null) }
    AppCard {
        SectionHeader(
            step = "02",
            label = "AGENT",
            status = if (runtime.running) "Running" else "Stopped",
            tone = if (runtime.running) ScreenTone.Success else ScreenTone.Neutral,
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            ConnectionTile(
                label = "Cellular",
                value = runtime.cellular.name.lowercase().replaceFirstChar(Char::uppercase),
                tone = when (runtime.cellular) {
                    CellularHealth.Available -> ScreenTone.Success
                    CellularHealth.Unavailable -> if (runtime.running) ScreenTone.Warning else ScreenTone.Neutral
                },
                modifier = Modifier.weight(1f),
            )
            ConnectionTile(
                label = "Clients",
                value = runtime.relay.name.lowercase().replaceFirstChar(Char::uppercase),
                tone = when (runtime.relay) {
                    RelayHealth.Connected -> ScreenTone.Info
                    RelayHealth.Connecting -> ScreenTone.Warning
                    RelayHealth.Disconnected -> ScreenTone.Neutral
                },
                modifier = Modifier.weight(1f),
            )
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            MetricTile("Streams", runtime.activeStreams.toString(), Modifier.weight(1f))
            MetricTile("Data up", formatByteCount(runtime.bytesUp), Modifier.weight(1f))
            MetricTile("Data down", formatByteCount(runtime.bytesDown), Modifier.weight(1f))
        }
        AgentMessage(runtime.errorClass)
        val addressRows = rotationAddressRows(runtime.rotation)
        if (addressRows.isNotEmpty()) {
            Surface(
                modifier = Modifier.fillMaxWidth(),
                color = MaterialTheme.colorScheme.surfaceVariant,
                shape = RoundedCornerShape(18.dp),
            ) {
                Column(
                    modifier = Modifier.padding(14.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    addressRows.forEach { row ->
                        Column(
                            modifier = Modifier.fillMaxWidth(),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            Text(
                                row.label,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                style = MaterialTheme.typography.bodySmall,
                            )
                            Text(
                                row.value,
                                color = MaterialTheme.colorScheme.onSurface,
                                style = MaterialTheme.typography.bodySmall,
                                fontWeight = FontWeight.SemiBold,
                            )
                        }
                    }
                }
            }
        }
        if (presentation.rotationAction != RotationAction.None) {
            val holdSeconds = if (presentation.rotationAction == RotationAction.Retry) 30 else 10
            Button(
                onClick = {
                    if (runtime.activeStreams > 0) {
                        pendingRotationSeconds = holdSeconds
                    } else {
                        onRotateIp(holdSeconds)
                    }
                },
                enabled = presentation.rotationEnabled,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 54.dp),
                shape = RoundedCornerShape(17.dp),
            ) {
                Text(presentation.rotationLabel)
            }
        }
        if (runtime.rotation.isActive()) {
            OutlinedButton(
                onClick = onCancelRotation,
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(17.dp),
            ) {
                Text("Cancel IP rotation")
            }
        }
        when (presentation.agentPrimaryAction) {
            AgentPrimaryAction.Start -> Button(
                onClick = onStart,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 54.dp),
                shape = RoundedCornerShape(17.dp),
            ) {
                Text("Start cellular Agent")
            }
            AgentPrimaryAction.Stop -> OutlinedButton(
                onClick = onStop,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 54.dp),
                shape = RoundedCornerShape(17.dp),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.error.copy(alpha = 0.7f)),
                colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.error),
            ) {
                Text("Stop Agent")
            }
            AgentPrimaryAction.None -> Surface(
                color = MaterialTheme.colorScheme.surfaceVariant,
                shape = RoundedCornerShape(17.dp),
            ) {
                Text(
                    text = presentation.inactiveAgentMessage,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 14.dp),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
        OutlinedButton(
            onClick = onCopyStatus,
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(17.dp),
        ) {
            Text("Copy diagnostic status")
        }
        pendingRotationSeconds?.let { holdSeconds ->
            AlertDialog(
                onDismissRequest = { pendingRotationSeconds = null },
                title = { Text("Disconnect active streams?") },
                text = {
                    Text(
                        "Rotating the cellular IP will disconnect ${runtime.activeStreams} active " +
                            if (runtime.activeStreams == 1) "stream." else "streams.",
                    )
                },
                confirmButton = {
                    TextButton(
                        onClick = {
                            pendingRotationSeconds = null
                            onRotateIp(holdSeconds)
                        },
                    ) {
                        Text("Disconnect and rotate")
                    }
                },
                dismissButton = {
                    TextButton(onClick = { pendingRotationSeconds = null }) {
                        Text("Keep connected")
                    }
                },
            )
        }
    }
}

@Composable
private fun AppCard(content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(28.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
    ) {
        Column(
            modifier = Modifier.padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
            content = content,
        )
    }
}

@Composable
private fun SectionHeader(
    step: String,
    label: String,
    status: String,
    tone: ScreenTone,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = step,
                color = MaterialTheme.colorScheme.primary,
                fontWeight = FontWeight.Bold,
                fontSize = 12.sp,
                letterSpacing = 1.sp,
            )
            Spacer(modifier = Modifier.width(8.dp))
            Text(
                text = label,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                fontWeight = FontWeight.Bold,
                fontSize = 12.sp,
                letterSpacing = 1.2.sp,
            )
        }
        StatusBadge(status, tone)
    }
}

@Composable
private fun StatusBadge(text: String, tone: ScreenTone) {
    val color = toneColor(tone)
    Surface(
        modifier = Modifier.widthIn(max = 168.dp),
        color = color.copy(alpha = 0.14f),
        contentColor = color,
        shape = RoundedCornerShape(999.dp),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                modifier = Modifier
                    .size(7.dp)
                    .background(color, CircleShape),
            )
            Spacer(modifier = Modifier.width(7.dp))
            Text(
                text = text,
                fontWeight = FontWeight.SemiBold,
                fontSize = 12.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun ConnectionTile(
    label: String,
    value: String,
    tone: ScreenTone,
    modifier: Modifier = Modifier,
) {
    val color = toneColor(tone)
    Surface(
        modifier = modifier,
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = RoundedCornerShape(20.dp),
    ) {
        Column(
            modifier = Modifier.padding(14.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(8.dp)
                        .background(color, CircleShape),
                )
                Spacer(modifier = Modifier.width(7.dp))
                Text(
                    text = label,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            Text(
                text = value,
                color = MaterialTheme.colorScheme.onSurface,
                fontWeight = FontWeight.SemiBold,
                fontSize = 16.sp,
            )
        }
    }
}

@Composable
private fun MetricTile(label: String, value: String, modifier: Modifier = Modifier) {
    Surface(
        modifier = modifier,
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = RoundedCornerShape(18.dp),
    ) {
        Column(
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = label,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                fontSize = 11.sp,
                maxLines = 1,
            )
            Text(
                text = value,
                color = MaterialTheme.colorScheme.onSurface,
                fontWeight = FontWeight.SemiBold,
                fontSize = 15.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun AgentMessage(errorClass: ErrorClass) {
    val hasError = errorClass != ErrorClass.None
    val color = toneColor(if (hasError) ScreenTone.Error else ScreenTone.Success)
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(8.dp)
                .background(color, CircleShape),
        )
        Spacer(modifier = Modifier.width(9.dp))
        Text(
            text = if (hasError) readableName(errorClass.name.replace("Relay", "Client")) else "No errors detected",
            color = if (hasError) color else MaterialTheme.colorScheme.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}

@Composable
private fun toneColor(tone: ScreenTone): Color = oledToneColor(tone)

private fun readableName(value: String): String = value
    .replace(Regex("([a-z])([A-Z])"), "$1 $2")
    .lowercase()
    .replaceFirstChar(Char::uppercase)

@Preview(
    name = "Unpaired dark",
    showBackground = true,
    widthDp = 360,
    heightDp = 800,
    uiMode = Configuration.UI_MODE_NIGHT_YES,
)
@Preview(
    name = "Unpaired narrow larger text",
    showBackground = true,
    widthDp = 320,
    heightDp = 800,
    fontScale = 1.5f,
    uiMode = Configuration.UI_MODE_NIGHT_YES,
)
@Composable
private fun UnpairedAgentPreview() {
    MobileEgressTheme {
        AgentScreen(
            state = MainUiState(),
            onScanQr = {},
            onCancelQrScan = {},
            onQrDecoded = {},
            onQrNotRecognized = {},
            onScannerUnavailable = {},
            onStart = {},
            onStop = {},
            onRotateIp = {},
            onCancelRotation = {},
            onCopyStatus = {},
        )
    }
}

@Preview(
    name = "Connected light",
    showBackground = true,
    widthDp = 360,
    heightDp = 800,
)
@Composable
private fun ConnectedAgentPreview() {
    MobileEgressTheme {
        AgentScreen(
            state = MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Connected,
                    activeStreams = 2,
                    bytesUp = 1536,
                    bytesDown = 2L * 1024 * 1024,
                ),
            ),
            onScanQr = {},
            onCancelQrScan = {},
            onQrDecoded = {},
            onQrNotRecognized = {},
            onScannerUnavailable = {},
            onStart = {},
            onStop = {},
            onRotateIp = {},
            onCancelRotation = {},
            onCopyStatus = {},
        )
    }
}
