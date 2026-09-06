export function canInstallNode(bridge, instance, managed, busy) {
  return bridge.ready && instance.ssmOnline && !managed && !busy
}

export function nextSetupStep(bridge, awsReady, nodes, { awsChecking = false, verified = false } = {}) {
  if (bridge.checking) return { tab: 'bridge', label: 'Checking bridge status', detail: 'Status checks are in progress.' }
  if (bridge.stale) return { tab: 'bridge', action: 'setup', button: 'Retry connection setup', label: 'Waiting for current bridge status', detail: 'The last known status is displayed while checks refresh. You can retry setup to repair the connection.' }
  if (bridge.tailscaleError && !bridge.tailscaleOnline) return { tab: 'bridge', action: 'refresh', button: 'Check again', label: 'Check Tailscale', detail: bridge.tailscaleError }
  if (bridge.needsRotation) return { tab: 'bridge', action: 'details', button: 'Review endpoint change', label: 'Review your bridge endpoint', detail: 'Your Tailscale address changed. Review the migration guidance before continuing.' }
  if (!bridge.ready) return { tab: 'bridge', action: 'setup', button: 'Set up this computer', label: 'Finish bridge setup', detail: 'We will connect Tailscale and set up your background relay. Approve the permissions when prompted.' }
  if (!bridge.agentConnected) {
    if (bridge.agentPaired === false) return { tab: 'phone', action: 'pair', button: 'Pair your phone', label: 'Pair your Agent', detail: 'Open the Android or iOS Agent and scan a pairing QR, then start cellular sharing.' }
    return { tab: 'phone', action: 'details', button: 'Open Agent setup', label: bridge.agentPaired ? 'Start cellular sharing' : 'Connect your Agent', detail: bridge.agentPaired ? 'Your Agent is paired. Open it on your phone and start sharing over cellular.' : 'Open your paired Agent and start cellular sharing. If this is a new phone, pair it with a QR.' }
  }
  if (awsChecking) return { tab: 'settings', label: 'Checking saved AWS connection', detail: 'Validating the saved connection and loading your instances.' }
  if (!awsReady) return { tab: 'settings', action: 'details', button: 'Connect AWS', label: 'Connect AWS', detail: 'Connect the account that contains your EC2 instance.' }
  if (!nodes.length || nodes.some(node => node.health === 'configuring')) return { tab: 'nodes', action: 'inventory', button: 'Load EC2 instances', label: 'Install your EC2 Client', detail: 'Select your instance, prepare its connection, and install. Use Repair for an interrupted configuration.' }
  if (verified) return { tab: 'nodes', complete: true, label: 'Setup complete', detail: 'You confirmed a successful request through your application proxy. Keep this computer and your Agent connected.' }
  return { tab: 'nodes', action: 'verify', button: 'Test your application', label: 'Verify connectivity', detail: 'Copy the proxy into your EC2 application, send a request, and confirm that it succeeds.' }
}
