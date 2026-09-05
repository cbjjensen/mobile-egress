export function canInstallNode(bridge, instance, managed, busy) {
  return bridge.ready && instance.ssmOnline && !managed && !busy
}

export function nextSetupStep(bridge, awsReady, nodes) {
  if (!bridge.tailscaleOnline && !bridge.ready) return { tab: 'bridge', label: 'Connect Tailscale', detail: 'Install Tailscale and finish its browser sign-in.' }
  if (!bridge.ready) return { tab: 'bridge', label: 'Finish bridge setup', detail: 'Complete or repair the local connection before installing an EC2 Client.' }
  if (!awsReady) return { tab: 'settings', label: 'Connect AWS', detail: 'Connect the account that contains your EC2 instance.' }
  if (!nodes.length || nodes.some(node => node.health === 'configuring')) return { tab: 'nodes', label: 'Install your EC2 Client', detail: 'Select your instance, prepare its connection, and install. Use Repair for an interrupted configuration.' }
  return { tab: 'nodes', label: 'Verify connectivity', detail: 'Pair your phone under Agent, keep cellular sharing running, then refresh node health. Confirm a request from your EC2 application succeeds through the copied proxy.' }
}
